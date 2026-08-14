package eventbus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/controlplane"
)

func drain(t *testing.T, ch <-chan controlplane.Event, n int) []controlplane.Event {
	t.Helper()
	events := make([]controlplane.Event, 0, n)
	for i := 0; i < n; i++ {
		select {
		case e := <-ch:
			events = append(events, e)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for event %d/%d", i+1, n)
		}
	}
	return events
}

func TestSubscribeFromZeroReceivesAllRetainedThenLive(t *testing.T) {
	bus := New(10)
	bus.Publish("info", "lifecycle", "provision", nil)
	bus.Publish("info", "lifecycle", "provision", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := bus.Subscribe(ctx, 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	bus.Publish("info", "lifecycle", "provision", nil)

	events := drain(t, ch, 3)
	for i, e := range events {
		if e.Sequence != uint64(i+1) {
			t.Fatalf("events out of order: %+v", events)
		}
	}
}

func TestSubscribeFromMidCursorSkipsAlreadySeen(t *testing.T) {
	bus := New(10)
	bus.Publish("info", "a", "one", nil)
	second := bus.Publish("info", "a", "two", nil)
	bus.Publish("info", "a", "three", nil)

	ch, err := bus.Subscribe(context.Background(), second.Sequence)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	events := drain(t, ch, 1)
	if events[0].Kind != "three" {
		t.Fatalf("expected only events after cursor, got %+v", events)
	}
}

func TestSubscribeReturnsHistoryGapWhenCursorEvicted(t *testing.T) {
	bus := New(2)
	bus.Publish("info", "a", "one", nil)
	bus.Publish("info", "a", "two", nil)
	bus.Publish("info", "a", "three", nil) // evicts "one"

	// Cursor 0 wants everything, including the now-evicted "one".
	_, err := bus.Subscribe(context.Background(), 0)
	if !errors.Is(err, controlplane.ErrEventHistoryGap) {
		t.Fatalf("expected ErrEventHistoryGap, got %v", err)
	}
}

func TestSubscribeReturnsHistoryGapForCursorBeyondAnythingPublished(t *testing.T) {
	bus := New(10)
	bus.Publish("info", "a", "one", nil)

	_, err := bus.Subscribe(context.Background(), 99)
	if !errors.Is(err, controlplane.ErrEventHistoryGap) {
		t.Fatalf("expected ErrEventHistoryGap, got %v", err)
	}
}

func TestSubscribeFreshBusWithZeroCursorIsNotAGap(t *testing.T) {
	bus := New(10)
	ch, err := bus.Subscribe(context.Background(), 0)
	if err != nil {
		t.Fatalf("Subscribe() on fresh bus error = %v", err)
	}
	bus.Publish("info", "a", "one", nil)
	events := drain(t, ch, 1)
	if events[0].Kind != "one" {
		t.Fatalf("unexpected event: %+v", events[0])
	}
}

func TestMultipleSubscribersEachReceiveEveryEvent(t *testing.T) {
	bus := New(10)
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	ch1, err := bus.Subscribe(ctx1, 0)
	if err != nil {
		t.Fatal(err)
	}
	ch2, err := bus.Subscribe(ctx2, 0)
	if err != nil {
		t.Fatal(err)
	}
	bus.Publish("info", "a", "one", nil)

	drain(t, ch1, 1)
	drain(t, ch2, 1)
}

func TestUnsubscribeClosesChannelOnContextCancel(t *testing.T) {
	bus := New(10)
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := bus.Subscribe(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("expected channel to close, got an event instead")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for channel to close after ctx cancel")
	}
}

func TestPublishNeverBlocksOnSlowSubscriber(t *testing.T) {
	bus := New(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := bus.Subscribe(ctx, 0); err != nil {
		t.Fatal(err)
	}
	// Never drain the subscriber channel; Publish must still return promptly
	// well past the channel's buffer capacity.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			bus.Publish("info", "a", "flood", nil)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Publish blocked on a slow/unread subscriber channel")
	}
}
