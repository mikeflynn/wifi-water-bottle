package tunnel

import (
	"bufio"
	"context"
	"io"
	"net"
	"testing"
)

func TestServeTLSRequestSelectsOnlyConfiguredKismetAndRelaysBytes(t *testing.T) {
	kismet, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer kismet.Close()
	relay, err := NewServer(kismet.Addr().String(), net.Dial)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		c, err := kismet.Accept()
		if err == nil {
			defer c.Close()
			_, _ = io.Copy(c, c)
		}
	}()
	client, server := net.Pipe()
	defer client.Close()
	done := make(chan error, 1)
	go func() { done <- relay.ServeTLSRequest(context.Background(), server, bufio.NewReader(server)) }()
	if _, err := client.Write([]byte("{\"type\":\"request\",\"op\":\"kismet_stream\"}\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("GET / HTTP/1.1\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("GET / HTTP/1.1\r\n\r\n"))
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "GET / HTTP/1.1\r\n\r\n" {
		t.Fatalf("echo = %q", got)
	}
	_ = client.Close()
	if err := <-done; err != nil && err != io.EOF {
		t.Fatal(err)
	}
}
