package model

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testEvent(sequence uint64, kind string) Event {
	return Event{
		Sequence:   sequence,
		OccurredAt: time.Date(2026, 8, 14, 9, 0, int(sequence), 0, time.UTC),
		Severity:   SeverityInfo,
		Source:     "kismet",
		Kind:       kind,
		Payload: map[string]any{
			"ssid":        "test-network",
			"token":       "must-not-leak",
			"recordCount": 1,
		},
	}
}

func TestBufferRedactsAndFiltersEvents(t *testing.T) {
	buffer := NewBuffer(BufferConfig{MaxEvents: 10, MaxBytes: 10_000})
	if err := buffer.Add(testEvent(1, KindRecordSummary)); err != nil {
		t.Fatal(err)
	}

	all := buffer.Visible()
	if len(all) != 1 {
		t.Fatalf("visible events = %d, want 1", len(all))
	}
	if got := all[0].Payload["token"]; got != RedactedValue {
		t.Fatalf("token = %#v, want redacted", got)
	}

	buffer.SetFilter(Filter{Severities: map[Severity]bool{SeverityError: true}})
	if got := buffer.Visible(); len(got) != 0 {
		t.Fatalf("filtered events = %d, want 0", len(got))
	}
}

func TestPauseOnlyStopsRendering(t *testing.T) {
	buffer := NewBuffer(BufferConfig{MaxEvents: 10, MaxBytes: 10_000})
	if err := buffer.Add(testEvent(1, KindStatus)); err != nil {
		t.Fatal(err)
	}
	buffer.SetPaused(true)
	paused := buffer.Visible()
	if err := buffer.Add(testEvent(2, KindStatus)); err != nil {
		t.Fatal(err)
	}
	if got := buffer.LastSequence(); got != 2 {
		t.Fatalf("cursor = %d, want 2", got)
	}
	if got := buffer.Visible(); len(got) != len(paused) {
		t.Fatalf("paused view changed from %d to %d events", len(paused), len(got))
	}
	buffer.SetPaused(false)
	if got := buffer.Visible(); len(got) != 2 {
		t.Fatalf("resumed view = %d, want 2", len(got))
	}
}

func TestBufferBoundsInsertsOverflowMarker(t *testing.T) {
	buffer := NewBuffer(BufferConfig{MaxEvents: 2, MaxBytes: 10_000})
	for i := uint64(1); i <= 3; i++ {
		if err := buffer.Add(testEvent(i, KindRecordSummary)); err != nil {
			t.Fatal(err)
		}
	}
	visible := buffer.Visible()
	if len(visible) != 3 {
		t.Fatalf("visible events = %d, want newest two plus marker", len(visible))
	}
	if visible[0].Kind != KindClientBufferOverflow {
		t.Fatalf("first event kind = %q, want overflow marker", visible[0].Kind)
	}
	if visible[1].Sequence != 2 || visible[2].Sequence != 3 {
		t.Fatalf("retained sequences = %d,%d, want 2,3", visible[1].Sequence, visible[2].Sequence)
	}
}

func TestBufferSustainsHighVolumeWithBoundedMemory(t *testing.T) {
	buffer := NewBuffer(BufferConfig{MaxEvents: 100, MaxBytes: 1 << 20})
	for i := uint64(1); i <= 10_000; i++ {
		if err := buffer.Add(testEvent(i, KindRecordSummary)); err != nil {
			t.Fatal(err)
		}
	}
	visible := buffer.Visible()
	if len(visible) != 101 {
		t.Fatalf("visible events = %d, want marker plus 100 retained events", len(visible))
	}
	if visible[0].Kind != KindClientBufferOverflow {
		t.Fatalf("first event kind = %q, want overflow marker", visible[0].Kind)
	}
	if visible[len(visible)-1].Sequence != 10_000 {
		t.Fatalf("latest sequence = %d, want 10000", visible[len(visible)-1].Sequence)
	}
}

func TestMalformedEventIsRejectedWithoutAdvancingCursor(t *testing.T) {
	buffer := NewBuffer(BufferConfig{MaxEvents: 10, MaxBytes: 10_000})
	event := testEvent(1, KindStatus)
	event.Source = ""
	if err := buffer.Add(event); err == nil {
		t.Fatal("Add() error = nil, want malformed event error")
	}
	if got := buffer.LastSequence(); got != 0 {
		t.Fatalf("cursor = %d, want 0", got)
	}
	visible := buffer.Visible()
	if len(visible) != 1 || visible[0].Kind != KindMalformedEvent {
		t.Fatalf("visible = %#v, want malformed marker", visible)
	}
}

func TestSaveNDJSONIsRedacted(t *testing.T) {
	buffer := NewBuffer(BufferConfig{MaxEvents: 10, MaxBytes: 10_000})
	if err := buffer.Add(testEvent(1, KindStatus)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "events.ndjson")
	if err := buffer.SaveNDJSON(path); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "must-not-leak") {
		t.Fatalf("saved event contains secret: %s", contents)
	}
	if !strings.Contains(string(contents), RedactedValue) {
		t.Fatalf("saved event omitted redaction: %s", contents)
	}
}

type scriptedStream struct {
	events []Event
	errs   []error
	index  int
}

func (s *scriptedStream) Recv() (Event, error) {
	if s.index < len(s.events) {
		event := s.events[s.index]
		s.index++
		return event, nil
	}
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		return Event{}, err
	}
	return Event{}, context.Canceled
}

func TestConsumeStreamsEventsAndUpdatesStatus(t *testing.T) {
	status := testEvent(1, KindStatus)
	status.Payload = map[string]any{"scan_status": "running", "token": "must-not-leak"}
	stream := &scriptedStream{events: []Event{status, testEvent(2, KindRecordSummary)}, errs: []error{context.Canceled}}
	client := NewClient(NewBuffer(BufferConfig{MaxEvents: 10, MaxBytes: 10_000}), func(context.Context, uint64) (Stream, error) {
		return stream, nil
	}, nil)
	if err := client.Consume(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Consume() error = %v, want context cancellation", err)
	}
	if got := client.Status().Survey; got != "running" {
		t.Fatalf("survey status = %q, want running", got)
	}
	if got := client.Buffer().LastSequence(); got != 2 {
		t.Fatalf("cursor = %d, want 2", got)
	}
}

func TestConsumeReconnectsFromCursorAndMarksHistoryGap(t *testing.T) {
	first := &scriptedStream{events: []Event{testEvent(1, KindStatus)}, errs: []error{errors.New("link lost")}}
	second := &scriptedStream{events: []Event{testEvent(2, KindSurvey)}, errs: []error{context.Canceled}}
	openCalls := make([]uint64, 0, 2)
	streams := []Stream{first, second}
	client := NewClient(NewBuffer(BufferConfig{MaxEvents: 10, MaxBytes: 10_000}), func(_ context.Context, after uint64) (Stream, error) {
		openCalls = append(openCalls, after)
		stream := streams[0]
		streams = streams[1:]
		return stream, nil
	}, func(context.Context) (StatusSnapshot, error) {
		return StatusSnapshot{Survey: "running"}, nil
	})
	client.SetReconnectDelay(0)
	if err := client.Consume(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Consume() error = %v, want context cancellation", err)
	}
	if len(openCalls) != 2 || openCalls[0] != 0 || openCalls[1] != 1 {
		t.Fatalf("open cursors = %v, want [0 1]", openCalls)
	}
	if got := client.Buffer().LastSequence(); got != 2 {
		t.Fatalf("cursor = %d, want 2", got)
	}
}

func TestConsumeResyncRefreshesStatusAndNeverHidesGap(t *testing.T) {
	stream := &scriptedStream{errs: []error{ErrResyncRequired, context.Canceled}}
	statusCalls := 0
	client := NewClient(NewBuffer(BufferConfig{MaxEvents: 10, MaxBytes: 10_000}), func(context.Context, uint64) (Stream, error) {
		return stream, nil
	}, func(context.Context) (StatusSnapshot, error) {
		statusCalls++
		return StatusSnapshot{Survey: "running"}, nil
	})
	client.SetReconnectDelay(0)
	if err := client.Consume(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Consume() error = %v, want context cancellation", err)
	}
	if statusCalls != 1 {
		t.Fatalf("status refreshes = %d, want 1", statusCalls)
	}
	found := false
	for _, event := range client.Buffer().Visible() {
		found = found || event.Kind == KindHistoryGap
	}
	if !found {
		t.Fatal("missing visible history-gap marker")
	}
}

func TestConsumeStopsPromptlyOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := NewClient(NewBuffer(BufferConfig{MaxEvents: 10, MaxBytes: 10_000}), func(context.Context, uint64) (Stream, error) {
		t.Fatal("opener must not be called for canceled context")
		return nil, nil
	}, nil)
	if err := client.Consume(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Consume() error = %v, want context cancellation", err)
	}
}
