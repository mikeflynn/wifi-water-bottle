package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/provisioncontrol"
)

type updateField int

const (
	updateFieldRequestID updateField = iota
	updateFieldVersion
	updateFieldChannel
	updateFieldCount
)

type updateModel struct {
	eng        *engine
	inputs     [updateFieldCount]textinput.Model
	focus      updateField
	spinner    spinner.Model
	submitting bool
	job        provisioncontrol.Job
	err        error
	haveResult bool
}

func newUpdateModel(eng *engine) updateModel {
	placeholders := [updateFieldCount]string{"update-2026-08-14", "v2", "stable"}
	m := updateModel{eng: eng}
	for i := range m.inputs {
		ti := textinput.New()
		ti.Placeholder = placeholders[i]
		ti.CharLimit = 128
		m.inputs[i] = ti
	}
	m.inputs[updateFieldChannel].SetValue("stable")
	m.inputs[0].Focus()
	s := spinner.New()
	s.Spinner = spinner.Dot
	m.spinner = s
	return m
}

type updateResultMsg struct {
	job provisioncontrol.Job
	err error
}

func updateSubmitCmd(eng *engine, req provisioncontrol.UpdateRequest) tea.Cmd {
	return func() tea.Msg {
		client := eng.clients.get()
		if client == nil {
			return updateResultMsg{err: fmt.Errorf("no paired profile loaded")}
		}
		ctrl := provisioncontrol.New(client)
		job, err := ctrl.Update(eng.ctx, req)
		return updateResultMsg{job: job, err: err}
	}
}

func (m updateModel) Update(msg tea.Msg) (updateModel, tea.Cmd) {
	switch msg := msg.(type) {
	case updateResultMsg:
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
		switch msg.String() {
		case "tab", "down":
			m.inputs[m.focus].Blur()
			m.focus = (m.focus + 1) % updateFieldCount
			m.inputs[m.focus].Focus()
			return m, nil
		case "shift+tab", "up":
			m.inputs[m.focus].Blur()
			m.focus = (m.focus - 1 + updateFieldCount) % updateFieldCount
			m.inputs[m.focus].Focus()
			return m, nil
		case "enter":
			req := provisioncontrol.UpdateRequest{
				RequestID: strings.TrimSpace(m.inputs[updateFieldRequestID].Value()),
				Version:   strings.TrimSpace(m.inputs[updateFieldVersion].Value()),
				Channel:   strings.TrimSpace(m.inputs[updateFieldChannel].Value()),
				Confirmed: true,
			}
			if req.RequestID == "" || req.Version == "" || m.eng.clients.get() == nil {
				return m, nil
			}
			m.submitting = true
			m.haveResult = false
			c := newTypedConfirm(
				"Confirm update",
				fmt.Sprintf("This activates release %q on channel %q, with automatic rollback if the post-update health check fails. Retype the version to proceed.", req.Version, req.Channel),
				req.Version, true, updateSubmitCmd(m.eng, req),
			)
			return m, requestConfirm(c)
		}
	}
	var cmd tea.Cmd
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	return m, cmd
}

func (m updateModel) Focused() bool { return true }

func (m updateModel) View() string {
	out := styleTitle.Render("Update") + "\n\n"
	if m.eng.clients.get() == nil {
		return out + styleDim.Render("pair a profile first (screen 2)")
	}
	labels := [updateFieldCount]string{"request id: ", "version:     ", "channel:     "}
	for i, in := range m.inputs {
		out += styleLabel.Render(labels[i]) + in.View() + "\n"
	}
	out += "\n" + styleDim.Render("tab moves fields, enter submits (requires typed confirmation)")
	if m.submitting {
		out += "\n\n" + m.spinner.View() + " updating..."
	}
	if m.haveResult {
		out += "\n\n" + renderJob(m.job, m.err)
	}
	return out
}
