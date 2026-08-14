package tunnel

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
)

type MTLSStreamOpener struct {
	address string
	config  *tls.Config
	dialer  net.Dialer
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func NewMTLSStreamOpener(address string, config *tls.Config) (*MTLSStreamOpener, error) {
	if _, _, err := net.SplitHostPort(address); err != nil {
		return nil, fmt.Errorf("invalid Pi control-plane address: %w", err)
	}
	if config == nil || config.InsecureSkipVerify || config.RootCAs == nil || len(config.Certificates) == 0 {
		return nil, fmt.Errorf("mTLS tunnel requires verified private CA and client certificate")
	}
	for _, certificate := range config.Certificates {
		if len(certificate.Certificate) == 0 || certificate.PrivateKey == nil {
			return nil, fmt.Errorf("mTLS tunnel client certificate is incomplete")
		}
	}
	clone := config.Clone()
	clone.MinVersion = tls.VersionTLS13
	if clone.ServerName == "" {
		host, _, _ := net.SplitHostPort(address)
		clone.ServerName = host
	}
	return &MTLSStreamOpener{address: address, config: clone}, nil
}
func (o *MTLSStreamOpener) Open(ctx context.Context) (io.ReadWriteCloser, error) {
	conn, err := o.dialer.DialContext(ctx, "tcp", o.address)
	if err != nil {
		return nil, fmt.Errorf("connect Pi control plane: %w", err)
	}
	tlsConn := tls.Client(conn, o.config)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mTLS tunnel handshake: %w", err)
	}
	reader := bufio.NewReader(tlsConn)
	dec := json.NewDecoder(reader)
	enc := json.NewEncoder(tlsConn)
	if err := enc.Encode(map[string]any{"type": "hello", "version": "bottle-control/1"}); err != nil {
		_ = tlsConn.Close()
		return nil, fmt.Errorf("send tunnel hello: %w", err)
	}
	var ack struct {
		Type  string `json:"type"`
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := dec.Decode(&ack); err != nil || !ack.OK {
		_ = tlsConn.Close()
		if err != nil {
			return nil, fmt.Errorf("read tunnel hello: %w", err)
		}
		return nil, fmt.Errorf("tunnel hello rejected: %s", ack.Error)
	}
	if err := enc.Encode(map[string]any{"type": "request", "version": "bottle-control/1", "id": "kismet-stream", "op": "tunnel"}); err != nil {
		_ = tlsConn.Close()
		return nil, fmt.Errorf("select Kismet tunnel: %w", err)
	}
	if err := dec.Decode(&ack); err != nil || !ack.OK {
		_ = tlsConn.Close()
		if err != nil {
			return nil, fmt.Errorf("read Kismet selection: %w", err)
		}
		return nil, fmt.Errorf("Kismet tunnel rejected: %s", ack.Error)
	}
	return &bufferedConn{Conn: tlsConn, reader: reader}, nil
}
func PinnedRoots(caPEM []byte) (*x509.CertPool, error) {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("invalid pinned Pi CA PEM")
	}
	return roots, nil
}
