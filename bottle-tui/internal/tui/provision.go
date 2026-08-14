package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/provisioncontrol"
)

type provisionModel struct {
	eng        *engine
	requestID  textinput.Model
	spinner    spinner.Model
	submitting bool
	job        provisioncontrol.Job
	err        error
	haveResult bool
}

func newProvisionModel(eng *engine) provisionModel {
	ti := textinput.New()
	ti.Placeholder = "provision-2026-08-14"
	ti.Focus()
	ti.CharLimit = 128
	s := spinner.New()
	s.Spinner = spinner.Dot
	return provisionModel{eng: eng, requestID: ti, spinner: s}
}

type provisionResultMsg struct {
	job provisioncontrol.Job
	err error
}

func provisionSubmitCmd(eng *engine, requestID string) tea.Cmd {
	return func() tea.Msg {
		client := eng.clients.get()
		if client == nil {
			return provisionResultMsg{err: fmt.Errorf("no paired profile loaded")}
		}
		ctrl := provisioncontrol.New(client)
		job, err := ctrl.Provision(eng.ctx, provisioncontrol.ProvisionRequest{RequestID: requestID, Confirmed: true})
		return provisionResultMsg{job: job, err: err}
	}
}

func (m provisionModel) Update(msg tea.Msg) (provisionModel, tea.Cmd) {
	switch msg := msg.(type) {
	case provisionResultMsg:
		m.submitting = false
		m.haveResult = true
		m.job, m.err = msg.job, msg.err
		return m, nil
	case spinner.TickMsg:
		if !m.submitting {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		if msg.String() == "enter" {
			id := strings.TrimSpace(m.requestID.Value())
			if id == "" || m.eng.clients.get() == nil {
				return m, nil
			}
			m.submitting = true
			m.haveResult = false
			c := newTypedConfirm(
				"Confirm provision",
				"This reconfigures the Pi: backup existing config, install packages, configure services, enable them, then health-check. Retype the request ID to proceed.",
				id, false, provisionSubmitCmd(m.eng, id),
			)
			return m, requestConfirm(c)
		}
	}
	var cmd tea.Cmd
	m.requestID, cmd = m.requestID.Update(msg)
	return m, cmd
}

func (m provisionModel) Focused() bool { return true }

func (m provisionModel) View() string {
	out := styleTitle.Render("Provision") + "\n\n"
	if m.eng.clients.get() == nil {
		return out + styleDim.Render("pair a profile first (screen 2)")
	}
	out += styleLabel.Render("request id: ") + m.requestID.View() + "\n\n"
	out += styleDim.Render("enter to submit (requires typed confirmation)")
	if m.submitting {
		out += "\n\n" + m.spinner.View() + " provisioning..."
	}
	if m.haveResult {
		out += "\n\n" + renderJob(m.job, m.err)
	}
	return out
}

func renderJob(job provisioncontrol.Job, err error) string {
	if err != nil {
		return styleError.Render("error: " + err.Error())
	}
	style := styleWarn
	lower := strings.ToLower(job.State)
	switch {
	case strings.Contains(lower, "rollback") || strings.Contains(lower, "fail"):
		style = styleError
	case strings.Contains(lower, "complete") || strings.Contains(lower, "succeed"):
		style = styleSuccess
	}
	out := styleLabel.Render("job:     ") + job.ID + "\n"
	out += styleLabel.Render("state:   ") + style.Render(job.State) + "\n"
	if job.Message != "" {
		out += styleLabel.Render("message: ") + job.Message
	}
	return out
}
