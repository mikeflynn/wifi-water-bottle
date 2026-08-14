package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/controlplane"
)

func renderHeader(width int, paired bool, surveyState string) string {
	badge := styleDim.Render("○ unpaired")
	if paired {
		badge = styleSuccess.Render("● paired")
	}
	survey := ""
	if surveyState != "" {
		survey = "  survey: " + surveyState
	}
	left := styleHeaderBar.Render("bottle-tui") + "  " + styleDim.Render(controlplane.PiAddress) + "  " + badge + survey
	return lipgloss.NewStyle().Width(width).Render(left)
}

func renderTabs(width int, active screen) string {
	out := ""
	for i, s := range screenOrder {
		label := screenTitles[s]
		if s == active {
			out += styleTabActive.Render(label)
		} else {
			out += styleTabIdle.Render(label)
		}
		if i < len(screenOrder)-1 {
			out += " "
		}
	}
	return out
}

func renderFooter(width int, helpView string) string {
	return styleFooter.Width(width).Render(helpView)
}
