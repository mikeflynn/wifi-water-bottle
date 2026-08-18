package controlplane

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/model"
)

// eventStream adapts one call to Client.StreamEvents (a channel pair) into
// the pull-based model.Stream interface expected by model.Client.Consume.
type eventStream struct {
	events <-chan Event
	errs   <-chan error
}

// Recv blocks for the next event or error. StreamEvents closes both channels
// together once the underlying connection ends, so once either channel
// reports closed, every subsequent Recv on this stream also returns io.EOF;
// the caller (model.Client.Consume) treats io.EOF as "reopen the stream" and
// calls OpenEventStream again, so this never spins.
func (s *eventStream) Recv() (model.Event, error) {
	select {
	case e, ok := <-s.events:
		if !ok {
			return model.Event{}, io.EOF
		}
		return e.Event, nil
	case err, ok := <-s.errs:
		if !ok {
			return model.Event{}, io.EOF
		}
		if errors.Is(err, ErrResyncRequired) {
			return model.Event{}, model.ErrResyncRequired
		}
		return model.Event{}, err
	}
}

// OpenEventStream returns a model.OpenStream bound to client, suitable for
// model.NewClient(buffer, OpenEventStream(client), FetchStatus(client)).
//
// Client.StreamEvents never fails synchronously (it always returns live
// channels and reports connection failures asynchronously on errs), so the
// returned OpenStream itself essentially never errors; failures surface one
// level down via Recv, which model.Client.Consume already treats the same
// way (wait-and-retry).
func OpenEventStream(c *Client) model.OpenStream {
	return func(ctx context.Context, afterSequence uint64) (model.Stream, error) {
		events, errs := c.StreamEvents(ctx, afterSequence)
		return &eventStream{events: events, errs: errs}, nil
	}
}

// FetchStatus adapts Client.Status to model.FetchStatus for use as the
// status refresher model.Client.Consume calls after a resync.
func FetchStatus(c *Client) model.FetchStatus {
	return func(ctx context.Context) (model.StatusSnapshot, error) {
		status, err := c.Status(ctx)
		if err != nil {
			return model.StatusSnapshot{}, err
		}
		return model.StatusSnapshot{Survey: status.Survey, GPSFix: status.GPSFix, ObservedAt: time.Now().UTC()}, nil
	}
}
