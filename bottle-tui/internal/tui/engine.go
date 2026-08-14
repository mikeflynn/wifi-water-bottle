package tui

import (
	"context"
	"sync"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/controlplane"
	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/model"
	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/wigle"
)

// clientHolder gives screen models a stable handle to the control-plane
// client even though it is created lazily (after a profile loads) and the
// App value itself is copied on every Update call, per Bubble Tea's Elm
// architecture.
type clientHolder struct {
	mu     sync.RWMutex
	client *controlplane.Client
}

func (h *clientHolder) get() *controlplane.Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.client
}

func (h *clientHolder) set(c *controlplane.Client) {
	h.mu.Lock()
	h.client = c
	h.mu.Unlock()
}

// AppDeps are the fakeable seams for credential storage and client
// construction, so tests can drive screens without touching a real OS
// keyring or network.
type AppDeps struct {
	LoadControlplaneCredentials func(ctx context.Context) (controlplane.Credentials, error)
	SaveControlplaneCredentials func(ctx context.Context, creds controlplane.Credentials) error
	NewControlplaneClient       func(address string, creds controlplane.Credentials) (*controlplane.Client, error)
	LoadWigleCredentials        func(ctx context.Context) (wigle.Credentials, error)
	SaveWigleCredentials        func(ctx context.Context, creds wigle.Credentials) error
}

// DefaultAppDeps wires the real OS keyring and network implementations.
func DefaultAppDeps() AppDeps {
	cpStore := controlplane.KeyringStore{}
	wigleStore := wigle.KeyringStore{}
	return AppDeps{
		LoadControlplaneCredentials: cpStore.Load,
		SaveControlplaneCredentials: cpStore.Save,
		NewControlplaneClient:       controlplane.NewClient,
		LoadWigleCredentials:        wigleStore.Load,
		SaveWigleCredentials:        wigleStore.Save,
	}
}

// engine bundles the shared, screen-independent handles every screen model
// needs to build its own tea.Cmds: a cancelable root context, the lazily
// populated control-plane client, the shared live-event buffer, and the
// fakeable dependency seams.
type engine struct {
	ctx     context.Context
	clients *clientHolder
	buf     *model.Buffer
	deps    AppDeps
}

func newEngine(ctx context.Context, deps AppDeps) *engine {
	return &engine{
		ctx:     ctx,
		clients: &clientHolder{},
		buf:     model.NewBuffer(model.BufferConfig{}),
		deps:    deps,
	}
}
