package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/model"
)

func TestLogsRenderShowsRedactedEvents(t *testing.T) {
	buf := model.NewBuffer(model.BufferConfig{})
	_ = buf.Add(model.Event{
		OccurredAt: time.Now().UTC(),
		Severity:   model.SeverityInfo,
		Source:     "kismet",
		Kind:       "status",
		Payload:    map[string]any{"token": "must-not-appear", "state": "running"},
	})
	m := newLogsModel(buf)
	m.SetSize(80, 10)
	m, _ = m.Update(refreshLogsMsg{})
	view := m.View()
	if strings.Contains(view, "must-not-appear") {
		t.Fatalf("expected redacted payload, got: %s", view)
	}
	if !strings.Contains(view, model.RedactedValue) {
		t.Fatalf("expected redaction marker in view: %s", view)
	}
}

func TestLogsHistoryGapRendersAsBanner(t *testing.T) {
	buf := model.NewBuffer(model.BufferConfig{})
	buf.MarkHistoryGap("server replay window expired")
	m := newLogsModel(buf)
	m.SetSize(80, 10)
	m, _ = m.Update(refreshLogsMsg{})
	if !strings.Contains(m.View(), "history gap") {
		t.Fatalf("expected history gap banner, got: %s", m.View())
	}
}

func TestLogsPauseFreezesBufferSnapshot(t *testing.T) {
	buf := model.NewBuffer(model.BufferConfig{})
	_ = buf.Add(model.Event{OccurredAt: time.Now().UTC(), Severity: model.SeverityInfo, Source: "kismet", Kind: "status"})
	m := newLogsModel(buf)
	m.SetSize(80, 10)
	m, _ = m.Update(keyMsgRune('p'))
	if !m.paused {
		t.Fatalf("expected paused state after 'p'")
	}
	_ = buf.Add(model.Event{OccurredAt: time.Now().UTC(), Severity: model.SeverityInfo, Source: "kismet", Kind: "record_summary"})
	m, _ = m.Update(refreshLogsMsg{})
	if strings.Contains(m.View(), "record_summary") {
		t.Fatalf("expected paused view to stay frozen, got: %s", m.View())
	}
}

func TestLogsConsumeStoppedShowsDisconnected(t *testing.T) {
	buf := model.NewBuffer(model.BufferConfig{})
	m := newLogsModel(buf)
	m.SetSize(80, 10)
	m, _ = m.Update(consumeStoppedMsg{})
	if !strings.Contains(m.View(), "disconnected") {
		t.Fatalf("expected disconnected badge, got: %s", m.View())
	}
}
