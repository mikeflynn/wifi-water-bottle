package tui

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/tunnel"
)

func TestTunnelDrainCollectsHistoryAndClosesOnChannelClose(t *testing.T) {
	opener := tunnel.StreamOpenerFunc(func(ctx context.Context) (io.ReadWriteCloser, error) {
		return nil, errors.New("no kismet available in test")
	})
	tun, err := tunnel.Start(context.Background(), 0, opener)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	handle := &tunnelHandle{t: tun}

	done := make(chan tea.Msg, 1)
	go func() { done <- drainTunnelCmd(handle)() }()

	// The starting event is emitted synchronously inside tunnel.Start, so it
	// is already queued; give the drain goroutine a moment to pick it up
	// before closing.
	time.Sleep(50 * time.Millisecond)
	_ = tun.Close()

	select {
	case msg := <-done:
		if _, ok := msg.(tunnelClosedMsg); !ok {
			t.Fatalf("expected tunnelClosedMsg, got %T", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("drain did not complete after Close")
	}

	hist := handle.snapshot()
	if len(hist) == 0 {
		t.Fatalf("expected at least the starting event in drained history")
	}
}

func TestTunnelHandleCapsHistoryAt200(t *testing.T) {
	h := &tunnelHandle{}
	for i := 0; i < 250; i++ {
		h.append(tunnel.Event{Status: tunnel.StatusConnected, Message: "x"})
	}
	if got := len(h.snapshot()); got != 200 {
		t.Fatalf("expected history capped at 200, got %d", got)
	}
}
