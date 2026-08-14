// Package tunnel implements the Pi side of the fixed-destination Kismet relay.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
)

// DialFunc dials the Kismet loopback endpoint. It permits deterministic tests
// without allowing the relay to select arbitrary destinations.
type DialFunc func(network, address string) (net.Conn, error)

// Server relays one authenticated control-plane stream to Kismet. KismetAddr is
// validated at construction and is never supplied by a remote client.
type Server struct {
	kismetAddr string
	dial       DialFunc
}

// NewServer creates a relay restricted to a literal loopback Kismet address.
// A hostname is intentionally rejected: configuration must be auditable and
// DNS must not be able to change the relay destination.
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

// KismetAddress returns the fixed, validated relay destination.
func (s *Server) KismetAddress() string { return s.kismetAddr }

// Serve relays one stream that has already passed the agent's TLS 1.3 mTLS
// authentication and paired-device authorization. It does not accept listeners
// and it cannot proxy to a caller-selected destination.
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
	copyBytes := func(dst io.Writer, src io.Reader) {
		_, err := io.Copy(dst, src)
		copyResult <- err
	}
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
