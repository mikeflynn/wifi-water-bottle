// Package controlplane implements the authenticated, typed laptop-to-Pi RPC.
package controlplane

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/model"
	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/provisioncontrol"
	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/tunnel"
)

const PiAddress = "10.77.0.1:7443"
const protocolVersion = "bottle-control/1"

var (
	ErrNotPaired      = errors.New("Pi rejected the laptop profile; pair it during the physical pairing window")
	ErrResyncRequired = errors.New("event history gap requires resync")
)

type Credentials struct {
	CAPEM, CertificatePEM, PrivateKeyPEM []byte
	ClientID                             string
}
type CredentialStore interface {
	Load(context.Context) (Credentials, error)
}

type Status struct {
	Ready   bool   `json:"ready"`
	Survey  string `json:"survey"`
	Message string `json:"message"`
}
type Event struct{ model.Event }

type wireMessage struct {
	Type         string          `json:"type"`
	Version      string          `json:"version,omitempty"`
	ID           string          `json:"id,omitempty"`
	Op           string          `json:"op,omitempty"`
	Confirm      bool            `json:"confirm,omitempty"`
	ClientID     string          `json:"client_id,omitempty"`
	LastSequence uint64          `json:"last_sequence,omitempty"`
	OK           bool            `json:"ok,omitempty"`
	Error        string          `json:"error,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	Sequence     uint64          `json:"sequence,omitempty"`
	Severity     model.Severity  `json:"severity,omitempty"`
	Source       string          `json:"source,omitempty"`
	Kind         string          `json:"kind,omitempty"`
	OccurredAt   time.Time       `json:"occurred_at,omitempty"`
}

type Client struct {
	address, clientID string
	tlsConfig         *tls.Config
	dialer            net.Dialer
	mu                sync.Mutex
}

func NewClient(address string, credentials Credentials) (*Client, error) {
	if address == "" {
		address = PiAddress
	}
	if address != PiAddress {
		return nil, fmt.Errorf("Pi control plane is fixed at %s", PiAddress)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(credentials.CAPEM) {
		return nil, errors.New("invalid pinned Pi CA PEM")
	}
	cert, err := tls.X509KeyPair(credentials.CertificatePEM, credentials.PrivateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("invalid client certificate: %w", err)
	}
	if credentials.ClientID == "" {
		return nil, errors.New("client profile ID is required")
	}
	return &Client{address: address, clientID: credentials.ClientID, tlsConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{cert}, ServerName: "10.77.0.1"}}, nil
}

func (c *Client) open(ctx context.Context) (*rpcConn, error) {
	conn, err := c.dialer.DialContext(ctx, "tcp", c.address)
	if err != nil {
		return nil, fmt.Errorf("connect Pi control plane at %s: %w", c.address, err)
	}
	t := tls.Client(conn, c.tlsConfig)
	if err := t.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("TLS 1.3 mTLS handshake: %w", err)
	}
	return &rpcConn{conn: t, enc: json.NewEncoder(t), dec: json.NewDecoder(bufio.NewReader(t))}, nil
}

type rpcConn struct {
	conn net.Conn
	enc  *json.Encoder
	dec  *json.Decoder
}

func (r *rpcConn) close() { _ = r.conn.Close() }
func (r *rpcConn) call(ctx context.Context, msg wireMessage) (wireMessage, error) {
	if err := r.enc.Encode(msg); err != nil {
		return wireMessage{}, err
	}
	result := make(chan struct {
		m   wireMessage
		err error
	}, 1)
	go func() {
		var m wireMessage
		err := r.dec.Decode(&m)
		result <- struct {
			m   wireMessage
			err error
		}{m, err}
	}()
	select {
	case <-ctx.Done():
		return wireMessage{}, ctx.Err()
	case v := <-result:
		if v.err != nil {
			return wireMessage{}, v.err
		}
		if !v.m.OK {
			if v.m.Error == "not_paired" {
				return wireMessage{}, ErrNotPaired
			}
			return wireMessage{}, errors.New(v.m.Error)
		}
		return v.m, nil
	}
}

func (c *Client) request(ctx context.Context, id, op string, confirm bool, payload any) (wireMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.open(ctx)
	if err != nil {
		return wireMessage{}, err
	}
	defer r.close()
	if _, err = r.call(ctx, wireMessage{Type: "hello", Version: protocolVersion, ClientID: c.clientID}); err != nil {
		return wireMessage{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return wireMessage{}, err
	}
	return r.call(ctx, wireMessage{Type: "request", Version: protocolVersion, ID: id, Op: op, Confirm: confirm, Payload: body})
}

func (c *Client) Provision(ctx context.Context, req provisioncontrol.ProvisionRequest) (provisioncontrol.Job, error) {
	m, err := c.request(ctx, req.RequestID, "provision", req.Confirmed, req)
	return decodeJob(m, err)
}
func (c *Client) Update(ctx context.Context, req provisioncontrol.UpdateRequest) (provisioncontrol.Job, error) {
	m, err := c.request(ctx, req.RequestID, "update", req.Confirmed, req)
	return decodeJob(m, err)
}
func decodeJob(m wireMessage, err error) (provisioncontrol.Job, error) {
	if err != nil {
		return provisioncontrol.Job{}, err
	}
	var j provisioncontrol.Job
	if err := json.Unmarshal(m.Payload, &j); err != nil {
		return j, fmt.Errorf("decode job: %w", err)
	}
	return j, nil
}
func (c *Client) Status(ctx context.Context) (Status, error) {
	m, err := c.request(ctx, "status", "status", false, nil)
	if err != nil {
		return Status{}, err
	}
	var s Status
	err = json.Unmarshal(m.Payload, &s)
	return s, err
}
func (c *Client) Survey(ctx context.Context, start bool, confirm bool) error {
	op := "survey_stop"
	if start {
		op = "survey_start"
	}
	_, err := c.request(ctx, op, op, confirm, nil)
	return err
}

// StreamEvents authenticates once and returns a cursor stream. A resync request is
// issued whenever the Pi reports a history gap; gaps are never hidden.
func (c *Client) StreamEvents(ctx context.Context, from uint64) (<-chan Event, <-chan error) {
	out := make(chan Event, 32)
	errs := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errs)
		r, err := c.open(ctx)
		if err != nil {
			errs <- err
			return
		}
		defer r.close()
		if _, err = r.call(ctx, wireMessage{Type: "hello", Version: protocolVersion, ClientID: c.clientID, LastSequence: from}); err != nil {
			errs <- err
			return
		}
		if err = r.enc.Encode(wireMessage{Type: "request", Version: protocolVersion, Op: "events_subscribe", LastSequence: from}); err != nil {
			errs <- err
			return
		}
		for {
			var m wireMessage
			if err := r.dec.Decode(&m); err != nil {
				if !errors.Is(err, io.EOF) && ctx.Err() == nil {
					errs <- err
				}
				return
			}
			if m.Type == "resync_required" {
				errs <- ErrResyncRequired
				return
			}
			if m.Type != "event" {
				continue
			}
			e := Event{Event: model.Event{Sequence: m.Sequence, OccurredAt: m.OccurredAt, Severity: m.Severity, Source: m.Source, Kind: m.Kind}}
			if len(m.Payload) > 0 {
				_ = json.Unmarshal(m.Payload, &e.Payload)
			}
			select {
			case out <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, errs
}

func NewMTLSOpener(c *Client) (tunnel.StreamOpener, error) {
	return tunnel.NewMTLSStreamOpener(c.address, c.tlsConfig)
}

var _ provisioncontrol.Client = (*Client)(nil)
