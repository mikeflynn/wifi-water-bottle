package controlplane

import (
	"errors"
	"io"
	"testing"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/model"
)

func TestEventStreamRecvDeliversEvent(t *testing.T) {
	events := make(chan Event, 1)
	errs := make(chan error, 1)
	events <- Event{Event: model.Event{Sequence: 1, Kind: "status"}}
	s := &eventStream{events: events, errs: errs}

	got, err := s.Recv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Sequence != 1 || got.Kind != "status" {
		t.Fatalf("unexpected event: %+v", got)
	}
}

func TestEventStreamRecvMapsResyncRequired(t *testing.T) {
	events := make(chan Event)
	errs := make(chan error, 1)
	errs <- ErrResyncRequired
	s := &eventStream{events: events, errs: errs}

	_, err := s.Recv()
	if !errors.Is(err, model.ErrResyncRequired) {
		t.Fatalf("expected model.ErrResyncRequired, got %v", err)
	}
}

func TestEventStreamRecvClosedChannelsReturnEOF(t *testing.T) {
	events := make(chan Event)
	errs := make(chan error)
	close(events)
	close(errs)
	s := &eventStream{events: events, errs: errs}

	_, err := s.Recv()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestEventStreamRecvPropagatesOtherErrors(t *testing.T) {
	events := make(chan Event)
	errs := make(chan error, 1)
	sentinel := errors.New("boom")
	errs <- sentinel
	s := &eventStream{events: events, errs: errs}

	_, err := s.Recv()
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}
