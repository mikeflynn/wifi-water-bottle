// Package tunnel exposes Kismet only through a laptop-loopback listener.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
)

var ErrLocalPortUnavailable = errors.New("LOCAL_PORT_UNAVAILABLE")

type Status string

const (
	StatusStarting     Status = "starting"
	StatusConnected    Status = "connected"
	StatusReconnecting Status = "reconnecting"
	StatusClosed       Status = "closed"
)

// Event is display-safe lifecycle information for the TUI.
type Event struct {
	Status  Status
	Message string
	Err     error
}

// StreamOpener obtains one authenticated, paired-device control-plane stream.
// Implementations must create a fresh mTLS-authorized stream for every call.
type StreamOpener interface {
	Open(context.Context) (io.ReadWriteCloser, error)
}

type StreamOpenerFunc func(context.Context) (io.ReadWriteCloser, error)

func (f StreamOpenerFunc) Open(ctx context.Context) (io.ReadWriteCloser, error) { return f(ctx) }

// Tunnel owns the local listener and its per-browser-connection relay streams.
type Tunnel struct {
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
	opener   StreamOpener
	wg       sync.WaitGroup

	mu        sync.RWMutex
	status    Status
	lastErr   error
	events    chan Event
	closeOnce sync.Once
}

// Start binds port on 127.0.0.1 only. Port zero requests an ephemeral port;
// a nonzero port collision returns ErrLocalPortUnavailable without fallback.
func Start(ctx context.Context, port int, opener StreamOpener) (*Tunnel, error) {
	if opener == nil {
		return nil, fmt.Errorf("authenticated stream opener is required")
	}
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("invalid local port %d", port)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLocalPortUnavailable, err)
	}
	childCtx, cancel := context.WithCancel(ctx)
	t := &Tunnel{
		listener: listener,
		ctx:      childCtx,
		cancel:   cancel,
		opener:   opener,
		status:   StatusStarting,
		events:   make(chan Event, 32),
	}
	t.emit(StatusStarting, "Kismet tunnel listening on "+listener.Addr().String(), nil)
	t.wg.Add(1)
	go t.acceptLoop()
	return t, nil
}

func (t *Tunnel) acceptLoop() {
	defer t.wg.Done()
	for {
		local, err := t.listener.Accept()
		if err != nil {
			if t.ctx.Err() == nil {
				t.emit(StatusReconnecting, "local Kismet listener stopped unexpectedly", err)
			}
			return
		}
		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			t.relay(local)
		}()
	}
}

func (t *Tunnel) relay(local net.Conn) {
	defer local.Close()
	stream, err := t.opener.Open(t.ctx)
	if err != nil {
		t.emit(StatusReconnecting, "Kismet tunnel transport lost; reconnecting on next request", err)
		return
	}
	defer stream.Close()
	t.emit(StatusConnected, "Kismet tunnel connected", nil)

	result := make(chan error, 2)
	copyBytes := func(dst io.Writer, src io.Reader) {
		_, err := io.Copy(dst, src)
		result <- err
	}
	go copyBytes(stream, local)
	go copyBytes(local, stream)
	select {
	case err := <-result:
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			t.emit(StatusReconnecting, "Kismet tunnel transport lost; reconnecting on next request", err)
		}
	case <-t.ctx.Done():
	}
}

func (t *Tunnel) emit(status Status, message string, err error) {
	t.mu.Lock()
	t.status, t.lastErr = status, err
	t.mu.Unlock()
	select {
	case t.events <- Event{Status: status, Message: message, Err: err}:
	default: // UI must not be able to stall an authenticated relay.
	}
}

func (t *Tunnel) ListenerAddr() net.Addr { return t.listener.Addr() }
func (t *Tunnel) URL() string            { return "http://" + t.listener.Addr().String() }
func (t *Tunnel) Events() <-chan Event   { return t.events }

func (t *Tunnel) Status() Status {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.status
}

func (t *Tunnel) LastError() error {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastErr
}

// Close stops accepting local connections and waits for active relays to exit.
func (t *Tunnel) Close() error {
	t.closeOnce.Do(func() {
		t.cancel()
		_ = t.listener.Close()
		t.wg.Wait()
		t.emit(StatusClosed, "Kismet tunnel closed", nil)
		close(t.events)
	})
	return nil
}
