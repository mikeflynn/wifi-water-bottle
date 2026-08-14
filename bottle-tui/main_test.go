package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/controlplane"
	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/model"
)

func TestRenderLiveLogsRedactsPayloadAndReportsHistoryGap(t *testing.T) {
	events := make(chan controlplane.Event, 1)
	errs := make(chan error, 1)
	events <- controlplane.Event{Event: model.Event{
		Sequence:   4,
		OccurredAt: time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC),
		Severity:   model.SeverityInfo,
		Source:     "kismet",
		Kind:       "status",
		Payload:    map[string]any{"token": "must-not-print", "state": "running"},
	}}
	close(events)
	errs <- controlplane.ErrResyncRequired
	close(errs)

	var output bytes.Buffer
	if err := renderLiveLogs(context.Background(), &output, events, errs); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.Contains(got, "[REDACTED]") || strings.Contains(got, "must-not-print") {
		t.Fatalf("live log did not redact sensitive payload: %s", got)
	}
	if !strings.Contains(got, model.KindHistoryGap) {
		t.Fatalf("live log did not surface history gap: %s", got)
	}
}
