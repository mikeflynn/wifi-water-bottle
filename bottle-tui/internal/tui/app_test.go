package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func testApp() App {
	ctx, cancel := context.WithCancel(context.Background())
	app := NewApp(ctx, cancel, testEngineDeps())
	app.width, app.height = 100, 40
	return app
}

func testEngineDeps() AppDeps {
	eng := testEngine()
	return eng.deps
}

func TestAppNumberKeyNavigatesWhenNotFocused(t *testing.T) {
	app := testApp()
	if app.active != screenDashboard {
		t.Fatalf("expected initial screen to be dashboard")
	}
	updated, _ := app.Update(keyMsgRune('3'))
	a := updated.(App)
	if a.active != screenProvision {
		t.Fatalf("expected number key to navigate to provision, got %v", a.active)
	}
}

func TestAppNumberKeySuppressedWhenFocused(t *testing.T) {
	app := testApp()
	updated, _ := app.Update(keyMsgRune(']')) // -> pairing (focused: has text inputs)
	a := updated.(App)
	if a.active != screenPairing {
		t.Fatalf("expected to land on pairing screen")
	}
	updated, _ = a.Update(keyMsgRune('3'))
	a2 := updated.(App)
	if a2.active != screenPairing {
		t.Fatalf("expected digit to be swallowed by the focused pairing field, stayed screen changed to %v", a2.active)
	}
}

func TestAppBracketNavAlwaysWorksEvenWhenFocused(t *testing.T) {
	app := testApp()
	updated, _ := app.Update(keyMsgRune(']')) // dashboard -> pairing
	a := updated.(App)
	if a.active != screenPairing {
		t.Fatalf("expected pairing, got %v", a.active)
	}
	updated, _ = a.Update(keyMsgRune(']')) // pairing -> provision
	a2 := updated.(App)
	if a2.active != screenProvision {
		t.Fatalf("expected bracket nav to advance past a focused screen, got %v", a2.active)
	}
}

func TestAppQuitCancelsContextWhenNoTunnel(t *testing.T) {
	app := testApp()
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd")
	}
	if app.eng.ctx.Err() == nil {
		t.Fatalf("expected root context to be canceled")
	}
}

func TestAppQuitPromptsConfirmWhenTunnelRunning(t *testing.T) {
	app := testApp()
	app.tunnel.running = true
	updated, cmd := app.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	a := updated.(App)
	if a.confirm == nil {
		t.Fatalf("expected quit to be gated behind a confirm modal while a tunnel is active")
	}
	if cmd != nil {
		t.Fatalf("expected no immediate quit cmd; must wait for confirmation")
	}
}
