package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type echoOpener struct{}

func (echoOpener) Open(context.Context) (io.ReadWriteCloser, error) {
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		_, _ = io.Copy(server, server)
	}()
	return client, nil
}

func TestMTLSStreamOpenerRejectsInsecureConfiguration(t *testing.T) {
	for _, config := range []*tls.Config{nil, {MinVersion: tls.VersionTLS13}, {MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{{}}}} {
		_, err := NewMTLSStreamOpener("10.77.0.1:7443", config)
		if err == nil {
			t.Fatal("NewMTLSStreamOpener accepted a config without a private CA and client certificate")
		}
	}
}

func TestStartBindsOnlyLaptopLoopbackAndRelaysHTTP(t *testing.T) {
	tunnel, err := Start(context.Background(), 0, echoOpener{})
	if err != nil {
		t.Fatal(err)
	}
	defer tunnel.Close()

	addr := tunnel.ListenerAddr().(*net.TCPAddr)
	if !addr.IP.IsLoopback() {
		t.Fatalf("listener bound to %v, want loopback only", addr)
	}
	if tunnel.URL() != "http://"+addr.String() {
		t.Fatalf("URL() = %q, want http://%s", tunnel.URL(), addr)
	}

	conn, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	request := "GET / HTTP/1.1\r\nHost: bottle\r\n\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(request))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != request {
		t.Fatalf("relay response = %q", got)
	}
}

func TestStartReturnsLocalPortUnavailable(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port

	_, err = Start(context.Background(), port, echoOpener{})
	if !errors.Is(err, ErrLocalPortUnavailable) {
		t.Fatalf("Start(port %d) error = %v, want ErrLocalPortUnavailable", port, err)
	}
}

func TestTunnelContinuesAcceptingAfterTransportLoss(t *testing.T) {
	var attempts atomic.Int32
	opener := StreamOpenerFunc(func(context.Context) (io.ReadWriteCloser, error) {
		if attempts.Add(1) == 1 {
			return nil, errors.New("mTLS stream lost")
		}
		return echoOpener{}.Open(context.Background())
	})
	tunnel, err := Start(context.Background(), 0, opener)
	if err != nil {
		t.Fatal(err)
	}
	defer tunnel.Close()

	addr := tunnel.ListenerAddr().String()
	first, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = first.Write([]byte("first"))
	_ = first.Close()

	deadline := time.Now().Add(time.Second)
	for tunnel.Status() != StatusReconnecting && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := tunnel.Status(); got != StatusReconnecting {
		t.Fatalf("status after transport loss = %q, want %q", got, StatusReconnecting)
	}

	second, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.Write([]byte("second")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("second"))
	if _, err := io.ReadFull(second, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Fatalf("reconnected response = %q", got)
	}
	if tunnel.Status() != StatusConnected {
		t.Fatalf("status after reconnect = %q, want %q", tunnel.Status(), StatusConnected)
	}
}
