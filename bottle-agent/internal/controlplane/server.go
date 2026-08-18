// Package controlplane is the Pi-side TLS 1.3 typed RPC endpoint.
package controlplane

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/lifecycle"
	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/tunnel"
)

const ListenAddress = "10.77.0.1:7443"
const protocolVersion = "bottle-control/1"

var ErrEventHistoryGap = errors.New("event history gap")

type Event struct {
	Sequence   uint64         `json:"sequence"`
	OccurredAt time.Time      `json:"occurred_at"`
	Severity   string         `json:"severity"`
	Source     string         `json:"source"`
	Kind       string         `json:"kind"`
	Payload    map[string]any `json:"payload,omitempty"`
}
type Status struct {
	Ready   bool   `json:"ready"`
	Survey  string `json:"survey"`
	GPSFix  bool   `json:"gps_fix"`
	Message string `json:"message"`
}
type Handler interface {
	Status(context.Context) (Status, error)
	Provision(context.Context, json.RawMessage, bool) (lifecycle.Job, error)
	Update(context.Context, json.RawMessage, bool) (lifecycle.Job, error)
	Survey(context.Context, bool, bool) error
	Events(context.Context, uint64) (<-chan Event, error)
}

type PairingStore struct {
	mu             sync.RWMutex
	allowed        map[string]bool
	physicalWindow bool
	path           string
}

func NewPairingStore() *PairingStore { return &PairingStore{allowed: map[string]bool{}} }
func NewPersistentPairingStore(path string) (*PairingStore, error) {
	p := &PairingStore{allowed: map[string]bool{}, path: path}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pairing allowlist: %w", err)
	}
	if err := json.Unmarshal(data, &p.allowed); err != nil {
		return nil, fmt.Errorf("decode pairing allowlist: %w", err)
	}
	return p, nil
}
func (p *PairingStore) OpenPhysicalWindow()  { p.mu.Lock(); p.physicalWindow = true; p.mu.Unlock() }
func (p *PairingStore) ClosePhysicalWindow() { p.mu.Lock(); p.physicalWindow = false; p.mu.Unlock() }
func (p *PairingStore) Pair(clientID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.physicalWindow {
		return errors.New("physical pairing window is closed")
	}
	if clientID == "" {
		return errors.New("client ID is required")
	}
	p.allowed[clientID] = true
	return p.persistLocked()
}
func (p *PairingStore) Allowed(clientID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.allowed[clientID]
}
func (p *PairingStore) persistLocked() error {
	if p.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(p.allowed)
	if err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}

type Server struct {
	listener  net.Listener
	tlsConfig *tls.Config
	pairings  *PairingStore
	handler   Handler
	tunnel    *tunnel.Server
	closeOnce sync.Once
}
type safeEncoder struct {
	mu   sync.Mutex
	enc  *json.Encoder
	conn io.ReadWriteCloser
}

func (e *safeEncoder) Encode(v any) error { e.mu.Lock(); defer e.mu.Unlock(); return e.enc.Encode(v) }

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func NewServer(address string, config *tls.Config, pairings *PairingStore, handler Handler) (*Server, error) {
	if address == "" {
		address = ListenAddress
	}
	if address != ListenAddress {
		return nil, fmt.Errorf("control plane is fixed at %s", ListenAddress)
	}
	if config == nil || config.MinVersion < tls.VersionTLS13 || config.ClientAuth != tls.RequireAndVerifyClientCert || config.ClientCAs == nil {
		return nil, errors.New("Pi control plane requires TLS 1.3 mTLS and private client CA")
	}
	if pairings == nil || handler == nil {
		return nil, errors.New("pairing store and handler are required")
	}
	return &Server{tlsConfig: config.Clone(), pairings: pairings, handler: handler}, nil
}
func (s *Server) SetKismetTunnel(relay *tunnel.Server) { s.tunnel = relay }
func (s *Server) ServeListener(listener net.Listener)  { s.listener = listener; go s.serve() }
func Listen(address string, config *tls.Config, pairings *PairingStore, handler Handler) (*Server, error) {
	s, err := NewServer(address, config, pairings, handler)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen control plane on eth0: %w", err)
	}
	s.listener = tls.NewListener(ln, s.tlsConfig)
	go s.serve()
	return s, nil
}
func (s *Server) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		if s.listener != nil {
			_ = s.listener.Close()
		}
	})
	return nil
}
func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return
	}
	if err := tlsConn.Handshake(); err != nil {
		return
	}
	clientID := certificateID(conn)
	if !s.pairings.Allowed(clientID) {
		writeMessage(conn, wireMessage{Type: "response", OK: false, Error: "not_paired"})
		return
	}
	reader := bufio.NewReader(conn)
	dec := json.NewDecoder(reader)
	stream := &bufferedConn{Conn: conn, reader: reader}
	enc := &safeEncoder{enc: json.NewEncoder(conn), conn: stream}
	var hello wireMessage
	if dec.Decode(&hello) != nil || hello.Type != "hello" || hello.Version != protocolVersion {
		_ = enc.Encode(wireMessage{Type: "response", OK: false, Error: "protocol_version"})
		return
	}
	if err := enc.Encode(wireMessage{Type: "hello_ack", OK: true, Version: protocolVersion}); err != nil {
		return
	}
	for {
		var req wireMessage
		if err := dec.Decode(&req); err != nil {
			return
		}
		if req.Type != "request" {
			continue
		}
		if req.Op == "tunnel" {
			if s.tunnel == nil {
				_ = enc.Encode(wireMessage{Type: "response", ID: req.ID, OK: false, Error: "Kismet tunnel is not configured"})
				return
			}
			if err := enc.Encode(wireMessage{Type: "response", ID: req.ID, OK: true}); err != nil {
				return
			}
			_ = s.tunnel.Serve(context.Background(), stream)
			return
		}
		if s.dispatch(context.Background(), enc, req) {
			return
		}
	}
}
func (s *Server) dispatch(ctx context.Context, enc *safeEncoder, req wireMessage) bool {
	reply := func(payload any, err error) {
		m := wireMessage{Type: "response", ID: req.ID, OK: err == nil}
		if err != nil {
			m.Error = err.Error()
		} else {
			m.Payload, _ = json.Marshal(payload)
		}
		_ = enc.Encode(m)
	}
	switch req.Op {
	case "status":
		reply(s.handler.Status(ctx))
	case "provision":
		if !req.Confirm {
			reply(nil, errors.New("explicit confirmation is required for provisioning"))
			return false
		}
		reply(s.handler.Provision(ctx, req.Payload, req.Confirm))
	case "update":
		if !req.Confirm {
			reply(nil, errors.New("explicit confirmation is required for updates"))
			return false
		}
		reply(s.handler.Update(ctx, req.Payload, req.Confirm))
	case "survey_start", "survey_stop":
		if !req.Confirm {
			reply(nil, errors.New("explicit confirmation is required for survey control"))
			return false
		}
		reply(nil, s.handler.Survey(ctx, req.Op == "survey_start", req.Confirm))
	case "events_subscribe":
		events, err := s.handler.Events(ctx, req.LastSequence)
		if err != nil {
			if errors.Is(err, ErrEventHistoryGap) {
				_ = enc.Encode(wireMessage{Type: "resync_required", ID: req.ID, Error: ErrEventHistoryGap.Error()})
			} else {
				reply(nil, err)
			}
			return false
		}
		go func() {
			for e := range events {
				payload, _ := json.Marshal(e.Payload)
				_ = enc.Encode(wireMessage{Type: "event", Sequence: e.Sequence, OccurredAt: e.OccurredAt, Severity: e.Severity, Source: e.Source, Kind: e.Kind, Payload: payload})
			}
		}()
	default:
		reply(nil, fmt.Errorf("unsupported typed operation %q", req.Op))
	}
	return false
}
func writeMessage(conn net.Conn, m wireMessage) { _ = json.NewEncoder(conn).Encode(m) }
func certificateID(conn net.Conn) string {
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return ""
	}
	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return ""
	}
	sum := sha256.Sum256(state.PeerCertificates[0].Raw)
	return hex.EncodeToString(sum[:])
}
func PinnedClientCA(pem []byte) (*x509.CertPool, error) {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("invalid client CA PEM")
	}
	return roots, nil
}

type wireMessage struct {
	Type         string          `json:"type"`
	Version      string          `json:"version,omitempty"`
	ID           string          `json:"id,omitempty"`
	Op           string          `json:"op,omitempty"`
	Confirm      bool            `json:"confirm,omitempty"`
	LastSequence uint64          `json:"last_sequence,omitempty"`
	OK           bool            `json:"ok,omitempty"`
	Error        string          `json:"error,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	Sequence     uint64          `json:"sequence,omitempty"`
	Severity     string          `json:"severity,omitempty"`
	Source       string          `json:"source,omitempty"`
	Kind         string          `json:"kind,omitempty"`
	OccurredAt   time.Time       `json:"occurred_at,omitempty"`
}
