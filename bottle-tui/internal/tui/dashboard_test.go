package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/controlplane"
	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/wigle"
)

func testEngine() *engine {
	return newEngine(context.Background(), AppDeps{
		LoadControlplaneCredentials: func(context.Context) (controlplane.Credentials, error) {
			return controlplane.Credentials{}, errors.New("secret not found in keyring")
		},
		SaveControlplaneCredentials: func(context.Context, controlplane.Credentials) error { return nil },
		NewControlplaneClient: func(address string, creds controlplane.Credentials) (*controlplane.Client, error) {
			return controlplane.NewClient(address, creds)
		},
		LoadWigleCredentials: func(context.Context) (wigle.Credentials, error) { return wigle.Credentials{}, nil },
		SaveWigleCredentials: func(context.Context, wigle.Credentials) error { return nil },
	})
}

func TestDashboardUnpairedShowsEmptyState(t *testing.T) {
	m := newDashboardModel(testEngine())
	updated, _ := m.Update(credentialsLoadedMsg{err: errors.New("secret not found in keyring")})
	if updated.paired {
		t.Fatalf("expected unpaired state")
	}
	if !strings.Contains(updated.View(), "paired yet") {
		t.Fatalf("expected empty-state hint, got: %s", updated.View())
	}
}

func TestDashboardStatusResultRendersSurveyState(t *testing.T) {
	m := newDashboardModel(testEngine())
	updated, _ := m.Update(statusResultMsg{status: controlplane.Status{Ready: true, Survey: "running", Message: "ok"}})
	updated.haveFetch = true
	updated.paired = true
	view := updated.View()
	if !strings.Contains(view, "running") {
		t.Fatalf("expected survey state in view: %s", view)
	}
	if !strings.Contains(view, "ready") {
		t.Fatalf("expected ready badge in view: %s", view)
	}
}

func TestDashboardRefreshRequiresPairing(t *testing.T) {
	m := newDashboardModel(testEngine())
	m.paired = false
	updated, cmd := m.Update(keyMsgRune('r'))
	if cmd != nil {
		t.Fatalf("expected no refresh cmd while unpaired")
	}
	if updated.loading {
		t.Fatalf("must not enter loading state while unpaired")
	}
}
