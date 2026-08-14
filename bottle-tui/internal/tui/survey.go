package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/controlplane"
	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/model"
)

type surveyModel struct {
	eng             *engine
	logs            logsModel
	consumerStarted bool
	lastAction      string
	lastErr         error
}

func newSurveyModel(eng *engine) surveyModel {
	return surveyModel{eng: eng, logs: newLogsModel(eng.buf)}
}

// Activate is called by App every time this screen becomes active. It only
// does real work the first time: kicking off the long-lived event consumer
// (model.Client.Consume, which blocks in its own tea.Cmd goroutine until the
// app's root context is canceled) and the viewport refresh tick.
func (m *surveyModel) Activate() tea.Cmd {
	if m.consumerStarted {
		return nil
	}
	m.consumerStarted = true
	return tea.Batch(m.logs.Start(), startEventConsumer(m.eng))
}

type surveyResultMsg struct {
	start bool
	err   error
}

func surveyControlCmd(eng *engine, start bool) tea.Cmd {
	return func() tea.Msg {
		client := eng.clients.get()
		if client == nil {
			return surveyResultMsg{start: start, err: fmt.Errorf("no paired profile loaded")}
		}
		err := client.Survey(eng.ctx, start, true)
		return surveyResultMsg{start: start, err: err}
	}
}

func startEventConsumer(eng *engine) tea.Cmd {
	return func() tea.Msg {
		client := eng.clients.get()
		if client == nil {
			return consumeStoppedMsg{err: fmt.Errorf("no paired profile loaded")}
		}
		mc := model.NewClient(eng.buf, controlplane.OpenEventStream(client), controlplane.FetchStatus(client))
		err := mc.Consume(eng.ctx)
		return consumeStoppedMsg{err: err}
	}
}

func (m surveyModel) Update(msg tea.Msg) (surveyModel, tea.Cmd) {
	switch msg := msg.(type) {
	case surveyResultMsg:
		if msg.err != nil {
			m.lastErr = msg.err
		} else {
			m.lastErr = nil
			action := "stop"
			if msg.start {
				action = "start"
			}
			m.lastAction = action + " requested"
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "s":
			if m.eng.clients.get() == nil {
				return m, nil
			}
			c := newYesNoConfirm("Confirm survey start", "This begins active collection on the Pi.", false, surveyControlCmd(m.eng, true))
			return m, requestConfirm(c)
		case "x":
			if m.eng.clients.get() == nil {
				return m, nil
			}
			c := newYesNoConfirm("Confirm survey stop", "This ends the in-progress survey on the Pi.", false, surveyControlCmd(m.eng, false))
			return m, requestConfirm(c)
		}
	}
	var cmd tea.Cmd
	m.logs, cmd = m.logs.Update(msg)
	return m, cmd
}

func (m *surveyModel) SetSize(width, height int) {
	m.logs.SetSize(width, height-2)
}

func (m surveyModel) Focused() bool { return false }

func (m surveyModel) View() string {
	if m.eng.clients.get() == nil {
		return styleTitle.Render("Survey") + "\n\n" + styleDim.Render("pair a profile first (screen 2)")
	}
	header := styleDim.Render("[s] start survey    [x] stop survey    [p] pause/resume scroll")
	if m.lastAction != "" {
		header += "\n" + styleSuccess.Render(m.lastAction)
	}
	if m.lastErr != nil {
		header += "\n" + styleError.Render(m.lastErr.Error())
	}
	return header + "\n\n" + m.logs.View()
}
