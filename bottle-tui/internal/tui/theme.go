// Package tui implements the interactive laptop operator console.
package tui

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/lipgloss"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/model"
)

var (
	colorBG       = lipgloss.Color("#0a0e0a")
	colorFG       = lipgloss.Color("#c8ffd4")
	colorGreen    = lipgloss.Color("#33ff66")
	colorGreenDim = lipgloss.Color("#1f9e44")
	colorAmber    = lipgloss.Color("#ffb454")
	colorRed      = lipgloss.Color("#ff5f5f")
	colorGrey     = lipgloss.Color("#7a8c7a")
	colorGreyDark = lipgloss.Color("#3a463a")
)

var (
	styleHeaderBar = lipgloss.NewStyle().Foreground(colorGreen).Bold(true).Padding(0, 1)
	styleTabActive = lipgloss.NewStyle().Foreground(colorBG).Background(colorGreen).Bold(true).Padding(0, 1)
	styleTabIdle   = lipgloss.NewStyle().Foreground(colorGreyDark).Padding(0, 1)
	stylePanel     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorGreenDim).Padding(1, 2)
	styleFooter    = lipgloss.NewStyle().Foreground(colorGrey).Padding(0, 1)
	styleTitle     = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	styleSuccess   = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	styleWarn      = lipgloss.NewStyle().Foreground(colorAmber).Bold(true)
	styleError     = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
	styleDim       = lipgloss.NewStyle().Foreground(colorGrey)
	styleLabel     = lipgloss.NewStyle().Foreground(colorGreenDim)

	styleConfirmDanger  = lipgloss.NewStyle().Border(lipgloss.ThickBorder()).BorderForeground(colorRed).Padding(1, 3)
	styleConfirmCaution = lipgloss.NewStyle().Border(lipgloss.ThickBorder()).BorderForeground(colorAmber).Padding(1, 3)
)

func severityStyle(s model.Severity) lipgloss.Style {
	switch s {
	case model.SeverityDebug:
		return styleDim
	case model.SeverityWarn:
		return styleWarn
	case model.SeverityError:
		return styleError
	default:
		return lipgloss.NewStyle().Foreground(colorFG)
	}
}

func newHelpModel() help.Model {
	h := help.New()
	h.Styles.ShortKey = lipgloss.NewStyle().Foreground(colorGreen)
	h.Styles.ShortDesc = lipgloss.NewStyle().Foreground(colorGrey)
	h.Styles.ShortSeparator = lipgloss.NewStyle().Foreground(colorGreyDark)
	h.Styles.FullKey = h.Styles.ShortKey
	h.Styles.FullDesc = h.Styles.ShortDesc
	h.Styles.FullSeparator = h.Styles.ShortSeparator
	return h
}
