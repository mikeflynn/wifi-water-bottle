package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/tui"
)

// runTUI launches the interactive operator console. Non-interactive usage
// (control/wigle subcommands) stays on the run() batch path unchanged.
func runTUI() error {
	// The theme is a fixed dark palette, not adaptive, so pin this rather
	// than let lipgloss lazily probe the terminal background on first
	// render: that probe sends an OSC 11 query and blocks reading a reply,
	// which never arrives on terminals/multiplexers that don't answer it.
	lipgloss.SetHasDarkBackground(true)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	app := tui.NewApp(ctx, cancel, tui.DefaultAppDeps())
	p := tea.NewProgram(app, tea.WithAltScreen())

	// signal.NotifyContext consumes the OS signal into ctx, which means the
	// process no longer dies from SIGINT/SIGTERM on its own; without this,
	// an external signal would cancel in-flight RPCs but never stop the
	// Bubble Tea event loop itself.
	go func() {
		<-ctx.Done()
		p.Quit()
	}()

	_, err := p.Run()
	return err
}
