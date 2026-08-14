package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/controlplane"
)

type dashboardModel struct {
	eng       *engine
	spinner   spinner.Model
	loading   bool
	paired    bool
	credErr   error
	status    controlplane.Status
	statusErr error
	haveFetch bool
}

func newDashboardModel(eng *engine) dashboardModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	return dashboardModel{eng: eng, spinner: s}
}

type credentialsLoadedMsg struct {
	creds controlplane.Credentials
	err   error
}
type statusResultMsg struct {
	status controlplane.Status
	err    error
}

func loadCredentialsCmd(eng *engine) tea.Cmd {
	return func() tea.Msg {
		creds, err := eng.deps.LoadControlplaneCredentials(eng.ctx)
		return credentialsLoadedMsg{creds: creds, err: err}
	}
}

func fetchStatusCmd(eng *engine) tea.Cmd {
	return func() tea.Msg {
		client := eng.clients.get()
		if client == nil {
			return statusResultMsg{err: fmt.Errorf("no paired profile loaded")}
		}
		status, err := client.Status(eng.ctx)
		return statusResultMsg{status: status, err: err}
	}
}

func (m dashboardModel) Update(msg tea.Msg) (dashboardModel, tea.Cmd) {
	switch msg := msg.(type) {
	case credentialsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.paired = false
			m.credErr = msg.err
			return m, nil
		}
		m.paired = true
		m.credErr = nil
		client, err := m.eng.deps.NewControlplaneClient(controlplane.PiAddress, msg.creds)
		if err != nil {
			m.statusErr = err
			return m, nil
		}
		m.eng.clients.set(client)
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, fetchStatusCmd(m.eng))
	case statusResultMsg:
		m.loading = false
		m.haveFetch = true
		m.status = msg.status
		m.statusErr = msg.err
		return m, nil
	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		if msg.String() == "r" && m.paired {
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, fetchStatusCmd(m.eng))
		}
	}
	return m, nil
}

func (m dashboardModel) Focused() bool { return false }

func (m dashboardModel) View() string {
	if !m.paired {
		msg := "No control-plane profile is paired yet."
		if m.credErr != nil {
			msg += "\n\n" + styleDim.Render(m.credErr.Error())
		}
		msg += "\n\nGo to the " + styleTitle.Render("pairing") + " screen to import a profile."
		return msg
	}
	if m.loading {
		return fmt.Sprintf("%s fetching status from %s...", m.spinner.View(), controlplane.PiAddress)
	}
	if m.statusErr != nil {
		return styleError.Render("status error: "+m.statusErr.Error()) + "\n\n" + styleDim.Render("press r to retry")
	}
	if !m.haveFetch {
		return styleDim.Render("press r to fetch status")
	}
	readyLine := styleSuccess.Render("ready")
	if !m.status.Ready {
		readyLine = styleWarn.Render("not ready")
	}
	out := styleLabel.Render("pi:      ") + controlplane.PiAddress + "\n"
	out += styleLabel.Render("state:   ") + readyLine + "\n"
	out += styleLabel.Render("survey:  ") + m.status.Survey + "\n"
	if m.status.Message != "" {
		out += styleLabel.Render("message: ") + m.status.Message + "\n"
	}
	out += "\n" + styleDim.Render("press r to refresh")
	return out
}
