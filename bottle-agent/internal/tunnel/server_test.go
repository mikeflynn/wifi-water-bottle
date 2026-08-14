package tunnel

import (
	"context"
	"io"
	"net"
	"testing"
)

func TestNewServerRejectsNonLoopbackKismetEndpoints(t *testing.T) {
	for _, endpoint := range []string{"0.0.0.0:2501", "10.77.0.1:2501", "[::]:2501"} {
		t.Run(endpoint, func(t *testing.T) {
			_, err := NewServer(endpoint, net.Dial)
			if err == nil {
				t.Fatalf("NewServer(%q) succeeded; want loopback-only validation failure", endpoint)
			}
		})
	}
}

func TestNewServerAcceptsLoopbackKismetEndpoint(t *testing.T) {
	server, err := NewServer("127.0.0.1:2501", net.Dial)
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}
	if got := server.KismetAddress(); got != "127.0.0.1:2501" {
		t.Fatalf("KismetAddress() = %q, want %q", got, "127.0.0.1:2501")
	}
}

func TestServeRelaysAuthenticatedStreamOnlyToKismetLoopback(t *testing.T) {
	kismet, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer kismet.Close()

	server, err := NewServer(kismet.Addr().String(), net.Dial)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		conn, err := kismet.Accept()
		if err == nil {
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}
	}()

	client, authenticatedStream := net.Pipe()
	defer client.Close()
	done := make(chan error, 1)
	go func() { done <- server.Serve(context.Background(), authenticatedStream) }()

	request := "GET / HTTP/1.1\r\n\r\n"
	if _, err := client.Write([]byte(request)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(request))
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != request {
		t.Fatalf("relay response = %q", got)
	}
	_ = client.Close()
	if err := <-done; err != nil && err != io.EOF {
		t.Fatalf("Serve returned %v", err)
	}
}
