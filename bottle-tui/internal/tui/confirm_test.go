package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestConfirmYesNoAccept(t *testing.T) {
	ran := false
	c := newYesNoConfirm("t", "b", false, func() tea.Msg { ran = true; return nil })
	updated, cmd, resolved := c.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if !resolved {
		t.Fatalf("expected resolved on y")
	}
	if cmd == nil {
		t.Fatalf("expected onConfirm cmd to be returned")
	}
	cmd()
	if !ran {
		t.Fatalf("onConfirm was not invoked")
	}
	_ = updated
}

func TestConfirmYesNoCancel(t *testing.T) {
	ran := false
	c := newYesNoConfirm("t", "b", false, func() tea.Msg { ran = true; return nil })
	_, cmd, resolved := c.update(tea.KeyMsg{Type: tea.KeyEsc})
	if !resolved {
		t.Fatalf("expected resolved on esc")
	}
	if cmd != nil {
		t.Fatalf("expected no cmd on cancel")
	}
	if ran {
		t.Fatalf("onConfirm must not run on cancel")
	}
}

func TestConfirmTypedRequiresExactMatch(t *testing.T) {
	ran := false
	c := newTypedConfirm("t", "b", "provision-42", false, func() tea.Msg { ran = true; return nil })

	for _, r := range "wrong-id" {
		c, _, _ = c.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	_, cmd, resolved := c.update(tea.KeyMsg{Type: tea.KeyEnter})
	if !resolved {
		t.Fatalf("expected resolved (canceled) on mismatched text")
	}
	if cmd != nil {
		t.Fatalf("expected no cmd when typed text does not match")
	}
	if ran {
		t.Fatalf("onConfirm must not run on mismatched text")
	}
}

func TestConfirmTypedAcceptsExactMatch(t *testing.T) {
	ran := false
	c := newTypedConfirm("t", "b", "provision-42", false, func() tea.Msg { ran = true; return nil })

	for _, r := range "provision-42" {
		c, _, _ = c.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	_, cmd, resolved := c.update(tea.KeyMsg{Type: tea.KeyEnter})
	if !resolved {
		t.Fatalf("expected resolved on exact match")
	}
	if cmd == nil {
		t.Fatalf("expected onConfirm cmd on exact match")
	}
	cmd()
	if !ran {
		t.Fatalf("onConfirm was not invoked")
	}
}
