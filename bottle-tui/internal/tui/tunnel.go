package tui

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/controlplane"
	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/tunnel"
)

// tunnelHandle owns the live *tunnel.Tunnel and a drained, mutex-protected
// history. Tunnel.Events() is buffered 32 with a silent non-blocking drop on
// backpressure, so it must be drained continuously by a dedicated goroutine
// rather than read on demand by the UI.
type tunnelHandle struct {
	t       *tunnel.Tunnel
	mu      sync.Mutex
	history []tunnel.Event
}

func (h *tunnelHandle) append(ev tunnel.Event) {
	h.mu.Lock()
	h.history = append(h.history, ev)
	if len(h.history) > 200 {
		h.history = h.history[len(h.history)-200:]
	}
	h.mu.Unlock()
}

func (h *tunnelHandle) snapshot() []tunnel.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]tunnel.Event(nil), h.history...)
}

type tunnelModel struct {
	eng     *engine
	port    textinput.Model
	handle  *tunnelHandle
	url     string
	running bool
	closing bool
	err     error
	history []tunnel.Event
}

func newTunnelModel(eng *engine) tunnelModel {
	ti := textinput.New()
	ti.SetValue("2501")
	ti.CharLimit = 5
	ti.Focus()
	return tunnelModel{eng: eng, port: ti}
}

type tunnelStartedMsg struct {
	handle *tunnelHandle
	url    string
	err    error
}
type tunnelClosedMsg struct{}
type tunnelTickMsg struct{}

func tickTunnel() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return tunnelTickMsg{} })
}

func tunnelStartCmd(eng *engine, port int) tea.Cmd {
	return func() tea.Msg {
		client := eng.clients.get()
		if client == nil {
			return tunnelStartedMsg{err: fmt.Errorf("no paired profile loaded")}
		}
		opener, err := controlplane.NewMTLSOpener(client)
		if err != nil {
			return tunnelStartedMsg{err: err}
		}
		t, err := tunnel.Start(eng.ctx, port, opener)
		if err != nil {
			return tunnelStartedMsg{err: err}
		}
		return tunnelStartedMsg{handle: &tunnelHandle{t: t}, url: t.URL()}
	}
}

func drainTunnelCmd(handle *tunnelHandle) tea.Cmd {
	return func() tea.Msg {
		for ev := range handle.t.Events() {
			handle.append(ev)
		}
		return tunnelClosedMsg{}
	}
}

func tunnelCloseCmd(handle *tunnelHandle) tea.Cmd {
	return func() tea.Msg {
		_ = handle.t.Close()
		return tunnelClosedMsg{}
	}
}

func (m tunnelModel) Update(msg tea.Msg) (tunnelModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tunnelStartedMsg:
		m.err = msg.err
		if msg.err != nil {
			return m, nil
		}
		m.handle = msg.handle
		m.url = msg.url
		m.running = true
		return m, tea.Batch(drainTunnelCmd(m.handle), tickTunnel())
	case tunnelTickMsg:
		if !m.running {
			return m, nil
		}
		m.history = m.handle.snapshot()
		return m, tickTunnel()
	case tunnelClosedMsg:
		m.running = false
		m.closing = false
		if m.handle != nil {
			m.history = m.handle.snapshot()
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "t":
			if m.running || m.eng.clients.get() == nil {
				return m, nil
			}
			port, err := strconv.Atoi(m.port.Value())
			if err != nil {
				m.err = fmt.Errorf("invalid port %q", m.port.Value())
				return m, nil
			}
			m.err = nil
			return m, tunnelStartCmd(m.eng, port)
		case "c":
			if !m.running || m.handle == nil {
				return m, nil
			}
			m.closing = true
			return m, tunnelCloseCmd(m.handle)
		}
	}
	if !m.running {
		var cmd tea.Cmd
		m.port, cmd = m.port.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m tunnelModel) Focused() bool { return !m.running }

func (m tunnelModel) View() string {
	out := styleTitle.Render("Kismet tunnel") + "\n\n"
	if m.eng.clients.get() == nil {
		return out + styleDim.Render("pair a profile first (screen 2)")
	}
	if !m.running {
		out += styleLabel.Render("local port: ") + m.port.View() + "\n\n"
		out += styleDim.Render("[t] start tunnel (loopback only, fixed Pi Kismet destination)")
		if m.err != nil {
			out += "\n\n" + styleError.Render(m.err.Error())
		}
		return out
	}
	status := styleSuccess.Render("connected")
	if m.closing {
		status = styleWarn.Render("closing")
	}
	out += styleLabel.Render("url:    ") + m.url + "\n"
	out += styleLabel.Render("status: ") + status + "\n\n"
	out += styleDim.Render("[c] close tunnel") + "\n\n"
	for _, ev := range m.history {
		out += renderTunnelEvent(ev) + "\n"
	}
	return out
}

func renderTunnelEvent(ev tunnel.Event) string {
	style := styleDim
	switch ev.Status {
	case tunnel.StatusConnected:
		style = styleSuccess
	case tunnel.StatusReconnecting:
		style = styleWarn
	}
	if ev.Err != nil {
		return styleError.Render(fmt.Sprintf("%s: %v", ev.Status, ev.Err))
	}
	return style.Render(fmt.Sprintf("%s: %s", ev.Status, ev.Message))
}
