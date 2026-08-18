package gpsd

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"
)

func startFakeGPSD(t *testing.T, respond func(conn net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		respond(conn)
	}()
	return ln.Addr().String()
}

func TestFixWatcherReportsFixOnTPVModeThreeOrGreater(t *testing.T) {
	addr := startFakeGPSD(t, func(conn net.Conn) {
		defer conn.Close()
		bufio.NewReader(conn).ReadString('\n') // consume the WATCH command
		conn.Write([]byte(`{"class":"TPV","mode":3}` + "\n"))
		time.Sleep(200 * time.Millisecond)
	})

	fixes := make(chan bool, 4)
	w := NewFixWatcher(addr, func(fix bool) { fixes <- fix })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	select {
	case fix := <-fixes:
		if !fix {
			t.Fatalf("expected first fix report to be true")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fix report")
	}
}

func TestFixWatcherSkipsMalformedLines(t *testing.T) {
	addr := startFakeGPSD(t, func(conn net.Conn) {
		defer conn.Close()
		bufio.NewReader(conn).ReadString('\n')
		conn.Write([]byte("not json\n"))
		conn.Write([]byte(`{"class":"TPV","mode":2}` + "\n"))
		time.Sleep(200 * time.Millisecond)
	})

	fixes := make(chan bool, 4)
	w := NewFixWatcher(addr, func(fix bool) { fixes <- fix })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	select {
	case fix := <-fixes:
		if !fix {
			t.Fatalf("expected the valid TPV report to be delivered")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fix report")
	}
}

func TestFixWatcherReportsNoFixAfterDisconnectThenReconnects(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	connCount := 0
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			connCount++
			n := connCount
			go func() {
				defer conn.Close()
				bufio.NewReader(conn).ReadString('\n')
				conn.Write([]byte(`{"class":"TPV","mode":3}` + "\n"))
				if n == 1 {
					time.Sleep(20 * time.Millisecond)
					return // close after first report: simulates a dropped connection
				}
				time.Sleep(200 * time.Millisecond)
			}()
		}
	}()

	var fixes []bool
	fixCh := make(chan bool, 8)
	w := NewFixWatcher(ln.Addr().String(), func(fix bool) { fixCh <- fix },
		WithBackoff(func(int) time.Duration { return time.Millisecond }))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	deadline := time.After(2 * time.Second)
	for len(fixes) < 3 {
		select {
		case fix := <-fixCh:
			fixes = append(fixes, fix)
		case <-deadline:
			t.Fatalf("timed out waiting for reconnect sequence, got %v", fixes)
		}
	}

	if fixes[0] != true || fixes[1] != false || fixes[2] != true {
		t.Fatalf("expected [true false true] (fix, disconnect, reconnect-fix), got %v", fixes)
	}
}
