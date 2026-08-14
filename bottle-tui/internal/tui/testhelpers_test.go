package tui

import tea "github.com/charmbracelet/bubbletea"

func keyMsgRune(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}
