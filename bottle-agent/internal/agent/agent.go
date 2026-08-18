// Package agent wires lifecycle.Provisioner/Updater, survey control, and the
// event bus into a concrete controlplane.Handler.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/controlplane"
	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/eventbus"
	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/lifecycle"
)

// Runner is the subset of lifecycle.Host used to start/stop the Kismet
// survey; *host.Host already satisfies this.
type Runner interface {
	Run(ctx context.Context, command string) error
}

// Handler implements controlplane.Handler.
//
// It does not yet hold a *lifecycle.Updater: Update() below is a stub. Doing
// real updates requires resolving a (version, channel) pair into a signed
// lifecycle.ReleaseManifest{URL, SHA256}, which needs a release-publishing
// format and hosting story that doesn't exist yet — deliberately out of
// scope for Pi-side bootstrap. lifecycle.Updater is otherwise ready to be
// wired in (see lifecycle.NewUpdater) once that exists.
type Handler struct {
	provisioner *lifecycle.Provisioner
	runner      Runner
	bus         *eventbus.Bus

	mu          sync.Mutex
	surveyState string
	gpsFix      bool
}

func New(provisioner *lifecycle.Provisioner, runner Runner, bus *eventbus.Bus) *Handler {
	return &Handler{provisioner: provisioner, runner: runner, bus: bus, surveyState: "idle"}
}

func (h *Handler) Status(context.Context) (controlplane.Status, error) {
	h.mu.Lock()
	survey := h.surveyState
	gpsFix := h.gpsFix
	h.mu.Unlock()
	return controlplane.Status{Ready: true, Survey: survey, GPSFix: gpsFix, Message: "bottle-agent running"}, nil
}

type provisionPayload struct{ RequestID string }

func (h *Handler) Provision(ctx context.Context, payload json.RawMessage, confirm bool) (lifecycle.Job, error) {
	var req provisionPayload
	if err := json.Unmarshal(payload, &req); err != nil {
		return lifecycle.Job{}, fmt.Errorf("decode provision request: %w", err)
	}
	job, err := h.provisioner.Run(ctx, lifecycle.ProvisionRequest{ID: req.RequestID, ConfirmConfigChanges: confirm})
	h.publishJob("provision", req.RequestID, job, err)
	return job, err
}

func (h *Handler) Update(ctx context.Context, payload json.RawMessage, confirm bool) (lifecycle.Job, error) {
	if !confirm {
		return lifecycle.Job{}, errors.New("explicit confirmation is required for updates")
	}
	// NOTE: resolving a (version, channel) request into a signed
	// lifecycle.ReleaseManifest{URL, SHA256} is not implemented yet — there
	// is no release-publishing pipeline for it to verify against.
	// lifecycle.Updater (see lifecycle.NewUpdater) is otherwise ready to be
	// wired in once that exists.
	return lifecycle.Job{}, errors.New("update channel resolution is not implemented yet")
}

func (h *Handler) Survey(ctx context.Context, start bool, confirm bool) error {
	if !confirm {
		return errors.New("explicit confirmation is required for survey control")
	}
	action, command, newState := "stop", "systemctl stop kismet", "idle"
	if start {
		action, command, newState = "start", "systemctl start kismet", "running"
	}
	if err := h.runner.Run(ctx, command); err != nil {
		h.bus.Publish("error", "survey", "survey_"+action+"_error", map[string]any{"error": err.Error()})
		return err
	}
	h.mu.Lock()
	h.surveyState = newState
	h.mu.Unlock()
	h.bus.Publish("info", "survey", "survey_"+action, map[string]any{"state": newState})
	return nil
}

// SetGPSFix records the current GPS fix state and publishes a
// gps_fix_acquired/gps_fix_lost event on each transition. Called by the
// gpsd fix watcher wired in main.go.
func (h *Handler) SetGPSFix(fix bool) {
	h.mu.Lock()
	changed := h.gpsFix != fix
	h.gpsFix = fix
	h.mu.Unlock()
	if !changed {
		return
	}
	kind := "gps_fix_lost"
	if fix {
		kind = "gps_fix_acquired"
	}
	h.bus.Publish("info", "gps", kind, map[string]any{"fix": fix})
}

// Shutdown publishes a power_button_shutdown event, then runs a clean
// poweroff. Called by the GPIO power button watcher wired in main.go.
func (h *Handler) Shutdown(ctx context.Context) error {
	h.bus.Publish("warn", "power", "power_button_shutdown", map[string]any{})
	return h.runner.Run(ctx, "systemctl poweroff")
}

func (h *Handler) Events(ctx context.Context, lastSequence uint64) (<-chan controlplane.Event, error) {
	return h.bus.Subscribe(ctx, lastSequence)
}

func (h *Handler) publishJob(kind, requestID string, job lifecycle.Job, err error) {
	if err != nil {
		h.bus.Publish("error", "lifecycle", kind+"_error", map[string]any{"request_id": requestID, "error": err.Error()})
		return
	}
	severity := "info"
	if job.State == lifecycle.Failed {
		severity = "error"
	}
	h.bus.Publish(severity, "lifecycle", kind+"_"+strings.ToLower(string(job.State)), map[string]any{
		"request_id": job.ID,
		"state":      string(job.State),
		"phase":      job.Phase,
		"message":    job.Message,
	})
}

var _ controlplane.Handler = (*Handler)(nil)
