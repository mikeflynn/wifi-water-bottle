package tunnel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
)

// DialFunc dials the Kismet loopback endpoint. It permits deterministic tests
// without allowing the relay to select arbitrary destinations.
type DialFunc func(network, address string) (net.Conn, error)

type Server struct {
	kismetAddr string
	dial       DialFunc
}

func NewServer(kismetAddr string, dial DialFunc) (*Server, error) {
	if dial == nil {
		return nil, fmt.Errorf("tunnel dialer is required")
	}
	host, port, err := net.SplitHostPort(kismetAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid Kismet endpoint: %w", err)
	}
	if port == "" {
		return nil, fmt.Errorf("Kismet endpoint requires a port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("Kismet endpoint must use a literal loopback address, got %q", host)
	}
	return &Server{kismetAddr: kismetAddr, dial: dial}, nil
}
func (s *Server) KismetAddress() string { return s.kismetAddr }

// Serve relays one stream that has already passed the agent's TLS 1.3 mTLS
// authentication and paired-device authorization. It cannot proxy to a caller-selected destination.
func (s *Server) Serve(ctx context.Context, authenticatedStream io.ReadWriteCloser) error {
	if authenticatedStream == nil {
		return fmt.Errorf("authenticated tunnel stream is required")
	}
	kismet, err := s.dial("tcp", s.kismetAddr)
	if err != nil {
		return fmt.Errorf("connect Kismet loopback endpoint: %w", err)
	}
	defer kismet.Close()
	defer authenticatedStream.Close()
	copyResult := make(chan error, 2)
	copyBytes := func(dst io.Writer, src io.Reader) { _, err := io.Copy(dst, src); copyResult <- err }
	go copyBytes(kismet, authenticatedStream)
	go copyBytes(authenticatedStream, kismet)
	select {
	case err := <-copyResult:
		if err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
			return nil
		}
		return fmt.Errorf("relay stream: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ServeTLSRequest consumes the typed stream-selection request and then relays
// the same authenticated TLS connection. The request is the only remote input
// used to select Kismet; the destination remains the constructor-pinned address.
func (s *Server) ServeTLSRequest(ctx context.Context, conn net.Conn, reader *bufio.Reader) error {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("decode tunnel selection: %w", err)
	}
	var request struct {
		Type string `json:"type"`
		Op   string `json:"op"`
	}
	if err := json.Unmarshal(line, &request); err != nil {
		return fmt.Errorf("decode tunnel selection: %w", err)
	}
	if request.Type != "request" || request.Op != "kismet_stream" {
		return fmt.Errorf("unsupported stream selection %q", request.Op)
	}
	return s.Serve(ctx, &bufferedConn{Conn: conn, reader: reader})
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
