package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// confirmModel is the single reusable confirmation modal for every
// confirm-gated action (provision, update, survey start/stop, WiGLE upload).
// Screens never dispatch a gated tea.Cmd directly; they hand it to the App
// via requestConfirmMsg and it only runs if this modal resolves accepted.
type confirmModel struct {
	title     string
	body      string
	danger    bool
	typedGate string
	input     textinput.Model
	onConfirm tea.Cmd
	errText   string
}

type requestConfirmMsg struct {
	confirm confirmModel
}

// requestConfirm hands a pre-built gated action to App, which only runs it
// if the modal resolves accepted. Screens never dispatch gated tea.Cmds
// directly.
func requestConfirm(c confirmModel) tea.Cmd {
	return func() tea.Msg { return requestConfirmMsg{confirm: c} }
}

func newYesNoConfirm(title, body string, danger bool, onConfirm tea.Cmd) confirmModel {
	return confirmModel{title: title, body: body, danger: danger, onConfirm: onConfirm}
}

func newTypedConfirm(title, body, mustType string, danger bool, onConfirm tea.Cmd) confirmModel {
	ti := textinput.New()
	ti.Placeholder = mustType
	ti.Focus()
	ti.CharLimit = 128
	return confirmModel{title: title, body: body, danger: danger, typedGate: mustType, input: ti, onConfirm: onConfirm}
}

// update handles one key message. resolved reports whether the modal should
// be dismissed; cmd (when non-nil, only on accept) is the gated action to run.
func (m confirmModel) update(msg tea.Msg) (confirmModel, tea.Cmd, bool) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		if m.typedGate != "" {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd, false
		}
		return m, nil, false
	}

	if m.typedGate != "" {
		switch keyMsg.String() {
		case "esc":
			return m, nil, true
		case "enter":
			if m.input.Value() == m.typedGate {
				return m, m.onConfirm, true
			}
			m.errText = "text does not match; action canceled"
			return m, nil, true
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd, false
		}
	}

	switch keyMsg.String() {
	case "y", "enter":
		return m, m.onConfirm, true
	case "n", "esc":
		return m, nil, true
	}
	return m, nil, false
}

func (m confirmModel) view(width int) string {
	style := styleConfirmCaution
	if m.danger {
		style = styleConfirmDanger
	}
	lines := []string{styleTitle.Render(m.title), "", m.body}
	if m.typedGate != "" {
		lines = append(lines, "", fmt.Sprintf("Type %q to confirm:", m.typedGate), m.input.View())
	} else {
		lines = append(lines, "", styleDim.Render("[y] confirm    [n/esc] cancel"))
	}
	box := lipgloss.JoinVertical(lipgloss.Left, lines...)
	modalWidth := width - 8
	if modalWidth > 72 {
		modalWidth = 72
	}
	if modalWidth < 20 {
		modalWidth = 20
	}
	return style.Width(modalWidth).Render(box)
}
