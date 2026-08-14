package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/model"
)

// logsModel is a reusable live-event viewport. It does not itself run the
// event consumer goroutine (see startEventConsumer in app.go); it only
// re-reads buffer.Visible() on a tick and renders it. This keeps rendering
// cheap and avoids turning every model.Event into its own tea.Msg.
type logsModel struct {
	buffer       *model.Buffer
	vp           viewport.Model
	width        int
	height       int
	paused       bool
	disconnected bool
	lastErr      error
	started      bool
}

func newLogsModel(buf *model.Buffer) logsModel {
	return logsModel{buffer: buf, vp: viewport.New(0, 0)}
}

type refreshLogsMsg struct{}
type consumeStoppedMsg struct{ err error }

func tickLogs() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return refreshLogsMsg{} })
}

// Start returns the tick command that keeps the viewport refreshed. Safe to
// call multiple times; the caller should only issue it once per screen-enter.
func (m *logsModel) Start() tea.Cmd {
	m.started = true
	m.disconnected = false
	return tickLogs()
}

func (m logsModel) Update(msg tea.Msg) (logsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case refreshLogsMsg:
		if !m.started {
			return m, nil
		}
		m.render()
		return m, tickLogs()
	case consumeStoppedMsg:
		m.disconnected = true
		m.lastErr = msg.err
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "p" {
			m.paused = !m.paused
			m.buffer.SetPaused(m.paused)
			m.render()
			return m, nil
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *logsModel) SetSize(width, height int) {
	m.width, m.height = width, height
	m.vp.Width = width
	m.vp.Height = height
	m.render()
}

func (m *logsModel) render() {
	events := m.buffer.Visible()
	lines := make([]string, 0, len(events))
	for _, e := range events {
		lines = append(lines, renderLogLine(e))
	}
	atBottom := m.vp.AtBottom()
	m.vp.SetContent(strings.Join(lines, "\n"))
	if atBottom && !m.paused {
		m.vp.GotoBottom()
	}
}

func renderLogLine(e model.Event) string {
	ts := e.OccurredAt.Format("15:04:05")
	switch e.Kind {
	case model.KindHistoryGap:
		return styleWarn.Render(fmt.Sprintf("%s  --- history gap: %v ---", ts, e.Payload["reason"]))
	case model.KindClientBufferOverflow:
		return styleWarn.Render(fmt.Sprintf("%s  --- buffer overflow: oldest events dropped ---", ts))
	case model.KindMalformedEvent:
		return styleError.Render(fmt.Sprintf("%s  --- malformed event: %v ---", ts, e.Payload["reason"]))
	}
	style := severityStyle(e.Severity)
	body := fmt.Sprintf("%s [%s] %s/%s", ts, strings.ToUpper(string(e.Severity)), e.Source, e.Kind)
	if len(e.Payload) > 0 {
		body += "  " + fmt.Sprint(e.Payload)
	}
	return style.Render(body)
}

func (m logsModel) View() string {
	status := styleSuccess.Render("live")
	if m.disconnected {
		status = styleError.Render("disconnected")
	} else if m.paused {
		status = styleWarn.Render("paused (scroll lock)")
	}
	header := styleLabel.Render("events: ") + status
	return lipgloss.JoinVertical(lipgloss.Left, header, m.vp.View())
}
