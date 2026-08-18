package gpio

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// fakeInputLine lets tests drive simulated button presses/releases. ready
// is closed once WatchEdges has registered fn, so tests don't race the
// goroutine running Run.
type fakeInputLine struct {
	ready chan struct{}
	fn    func(active bool)
}

func newFakeInputLine() *fakeInputLine {
	return &fakeInputLine{ready: make(chan struct{})}
}

func (f *fakeInputLine) WatchEdges(ctx context.Context, fn func(active bool)) error {
	f.fn = fn
	close(f.ready)
	<-ctx.Done()
	return nil
}

func (f *fakeInputLine) press()   { f.fn(true) }
func (f *fakeInputLine) release() { f.fn(false) }

func TestButtonFiresAfterHeldPastDuration(t *testing.T) {
	line := newFakeInputLine()
	var fired int32
	b := NewButton(line, 30*time.Millisecond, func() { atomic.AddInt32(&fired, 1) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	<-line.ready

	line.press()
	time.Sleep(60 * time.Millisecond)

	if got := atomic.LoadInt32(&fired); got != 1 {
		t.Fatalf("expected onHeld to fire once, got %d", got)
	}
}

func TestButtonDoesNotFireOnShortPress(t *testing.T) {
	line := newFakeInputLine()
	var fired int32
	b := NewButton(line, 50*time.Millisecond, func() { atomic.AddInt32(&fired, 1) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	<-line.ready

	line.press()
	time.Sleep(10 * time.Millisecond)
	line.release()
	time.Sleep(60 * time.Millisecond)

	if got := atomic.LoadInt32(&fired); got != 0 {
		t.Fatalf("expected onHeld not to fire on short press, got %d", got)
	}
}

func TestButtonFiresAgainAfterReleaseAndReHold(t *testing.T) {
	line := newFakeInputLine()
	var fired int32
	b := NewButton(line, 20*time.Millisecond, func() { atomic.AddInt32(&fired, 1) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	<-line.ready

	line.press()
	time.Sleep(40 * time.Millisecond)
	line.release()
	time.Sleep(10 * time.Millisecond)
	line.press()
	time.Sleep(40 * time.Millisecond)

	if got := atomic.LoadInt32(&fired); got != 2 {
		t.Fatalf("expected onHeld to fire twice, got %d", got)
	}
}
