// Package eventbus is the Pi-side sibling of bottle-tui/internal/model.Buffer:
// a bounded, sequence-numbered, multi-subscriber event store backing
// controlplane.Handler.Events. Where the laptop-side Buffer is a single
// consumer reading a local slice, this is pub-sub — multiple concurrent
// control-plane connections can each subscribe from their own cursor.
package eventbus

import (
	"context"
	"sync"
	"time"

	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/controlplane"
)

const defaultMaxEvents = 2000

// Bus is safe for concurrent use.
type Bus struct {
	mu           sync.Mutex
	maxEvents    int
	retained     []controlplane.Event // oldest first
	nextSequence uint64
	subs         map[uint64]chan controlplane.Event
	nextSubID    uint64
}

// New builds a Bus retaining at most maxEvents; maxEvents <= 0 uses a
// sensible default.
func New(maxEvents int) *Bus {
	if maxEvents <= 0 {
		maxEvents = defaultMaxEvents
	}
	return &Bus{maxEvents: maxEvents, subs: map[uint64]chan controlplane.Event{}}
}

// Publish assigns the next sequence number, retains the event, and fans it
// out to every live subscriber. A slow subscriber's channel is never
// blocked on — Publish must not stall the provisioning/update/survey path
// that calls it — so a subscriber that can't keep up misses events rather
// than backpressuring the agent; that gap is exactly what the sequence
// cursor and ErrEventHistoryGap on the next Subscribe call are for.
func (b *Bus) Publish(severity, source, kind string, payload map[string]any) controlplane.Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextSequence++
	event := controlplane.Event{
		Sequence:   b.nextSequence,
		OccurredAt: time.Now().UTC(),
		Severity:   severity,
		Source:     source,
		Kind:       kind,
		Payload:    payload,
	}
	b.retained = append(b.retained, event)
	if len(b.retained) > b.maxEvents {
		b.retained = b.retained[len(b.retained)-b.maxEvents:]
	}
	for _, ch := range b.subs {
		select {
		case ch <- event:
		default:
		}
	}
	return event
}

// Subscribe returns a channel delivering every retained event with
// Sequence > afterSequence, followed by new events as they're published.
// It returns controlplane.ErrEventHistoryGap if afterSequence is older than
// the oldest retained event (or requests a cursor beyond anything ever
// published), matching the resync contract bottle-tui/internal/model
// already expects from the wire protocol. The returned channel is closed
// when ctx is done.
func (b *Bus) Subscribe(ctx context.Context, afterSequence uint64) (<-chan controlplane.Event, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if afterSequence > b.nextSequence {
		return nil, controlplane.ErrEventHistoryGap
	}
	if len(b.retained) > 0 {
		oldest := b.retained[0].Sequence
		if afterSequence+1 < oldest {
			return nil, controlplane.ErrEventHistoryGap
		}
	}

	var backlog []controlplane.Event
	for _, e := range b.retained {
		if e.Sequence > afterSequence {
			backlog = append(backlog, e)
		}
	}

	// Buffered large enough to hold the backlog without blocking, and
	// registered in b.subs before we release the lock, so no concurrent
	// Publish can be interleaved ahead of or between backlog events.
	ch := make(chan controlplane.Event, len(backlog)+64)
	for _, e := range backlog {
		ch <- e
	}

	id := b.nextSubID
	b.nextSubID++
	b.subs[id] = ch

	go func() {
		<-ctx.Done()
		b.unsubscribe(id)
	}()

	return ch, nil
}

func (b *Bus) unsubscribe(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subs[id]; ok {
		delete(b.subs, id)
		close(ch)
	}
}
