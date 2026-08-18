package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/eventbus"
	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/lifecycle"
)

// fakeHost is a minimal lifecycle.Host satisfying only what Provisioner
// needs for these tests, mirroring the style of lifecycle_test.go's own
// unexported fakeHost (not reusable across packages).
type fakeHost struct{}

func (fakeHost) Architecture() string                          { return "arm64" }
func (fakeHost) IsBookworm() bool                              { return true }
func (fakeHost) FreeStorageMB() int                            { return 4096 }
func (fakeHost) HasEthernet(name string) bool                  { return name == "eth0" }
func (fakeHost) RadioCount() int                               { return 1 }
func (fakeHost) GPSVisible() bool                              { return true }
func (fakeHost) Config(string) (string, bool)                  { return "", false }
func (fakeHost) Backup([]string) error                         { return nil }
func (fakeHost) HealthCheck() error                            { return nil }
func (fakeHost) Fetch(context.Context, string) ([]byte, error) { return nil, nil }
func (fakeHost) Stage(string, []byte) error                    { return nil }
func (fakeHost) ActiveRelease() string                         { return "" }
func (fakeHost) Activate(string) error                         { return nil }
func (fakeHost) Rollback(string) error                         { return nil }

type fakeRunner struct {
	commands []string
	err      error
}

func (r *fakeRunner) Run(_ context.Context, command string) error {
	r.commands = append(r.commands, command)
	return r.err
}

// combinedHost adds Run (promoted from *fakeRunner) to fakeHost so it
// satisfies lifecycle.Host too.
type combinedHost struct {
	fakeHost
	*fakeRunner
}

func newHandler() (*Handler, *fakeRunner, *eventbus.Bus) {
	runner := &fakeRunner{}
	host := combinedHost{fakeRunner: runner}
	jobs := lifecycle.NewMemoryJobs()
	provisioner := lifecycle.NewProvisioner(host, jobs)
	bus := eventbus.New(50)
	return New(provisioner, runner, bus), runner, bus
}

func TestProvisionDecodesPayloadAndPublishesJobEvent(t *testing.T) {
	h, _, bus := newHandler()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := bus.Subscribe(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(struct{ RequestID string }{RequestID: "provision-1"})
	job, err := h.Provision(context.Background(), payload, true)
	if err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	if job.State != lifecycle.Succeeded {
		t.Fatalf("job state = %s, want Succeeded", job.State)
	}

	select {
	case event := <-ch:
		if event.Kind != "provision_succeeded" {
			t.Fatalf("unexpected event kind: %s", event.Kind)
		}
	default:
		t.Fatalf("expected a published event")
	}
}

func TestProvisionRejectsMalformedPayload(t *testing.T) {
	h, _, _ := newHandler()
	_, err := h.Provision(context.Background(), json.RawMessage(`not-json`), true)
	if err == nil {
		t.Fatalf("expected decode error")
	}
}

func TestUpdateIsNotYetImplemented(t *testing.T) {
	h, _, _ := newHandler()
	if _, err := h.Update(context.Background(), nil, true); err == nil {
		t.Fatalf("expected update to report not implemented")
	}
	if _, err := h.Update(context.Background(), nil, false); err == nil {
		t.Fatalf("expected confirmation-required error to take precedence")
	}
}

func TestSurveyStartAndStopRunCommandsAndPublishEvents(t *testing.T) {
	h, runner, bus := newHandler()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := bus.Subscribe(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := h.Survey(context.Background(), true, true); err != nil {
		t.Fatalf("Survey(start) error = %v", err)
	}
	status, _ := h.Status(context.Background())
	if status.Survey != "running" {
		t.Fatalf("status.Survey = %q, want running", status.Survey)
	}

	if err := h.Survey(context.Background(), false, true); err != nil {
		t.Fatalf("Survey(stop) error = %v", err)
	}
	status, _ = h.Status(context.Background())
	if status.Survey != "idle" {
		t.Fatalf("status.Survey = %q, want idle", status.Survey)
	}

	if len(runner.commands) != 2 || runner.commands[0] != "systemctl start kismet" || runner.commands[1] != "systemctl stop kismet" {
		t.Fatalf("unexpected commands: %#v", runner.commands)
	}

	for i := 0; i < 2; i++ {
		select {
		case <-ch:
		default:
			t.Fatalf("expected %d published survey events", 2)
		}
	}
}

func TestSurveyRequiresConfirmation(t *testing.T) {
	h, runner, _ := newHandler()
	if err := h.Survey(context.Background(), true, false); err == nil {
		t.Fatalf("expected confirmation-required error")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("expected no command to run without confirmation")
	}
}

func TestSetGPSFixPublishesEventOnTransitionAndUpdatesStatus(t *testing.T) {
	h, _, bus := newHandler()
	ctx := context.Background()
	events, err := bus.Subscribe(ctx, 0)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	h.SetGPSFix(true)

	select {
	case e := <-events:
		if e.Kind != "gps_fix_acquired" {
			t.Fatalf("expected gps_fix_acquired, got %s", e.Kind)
		}
	default:
		t.Fatal("expected an event to be published")
	}

	status, err := h.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.GPSFix {
		t.Fatal("expected Status().GPSFix to be true")
	}
}

func TestSetGPSFixDoesNotRepublishOnNoChange(t *testing.T) {
	h, _, bus := newHandler()
	ctx := context.Background()
	h.SetGPSFix(true)

	events, err := bus.Subscribe(ctx, 1) // after the first (setup) event
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	h.SetGPSFix(true) // no transition, should not publish

	select {
	case e := <-events:
		t.Fatalf("expected no event on unchanged fix state, got %+v", e)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestShutdownPublishesEventAndRunsPoweroff(t *testing.T) {
	h, runner, bus := newHandler()
	ctx := context.Background()
	events, err := bus.Subscribe(ctx, 0)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if err := h.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	if len(runner.commands) != 1 || runner.commands[0] != "systemctl poweroff" {
		t.Fatalf("expected systemctl poweroff to run, got %v", runner.commands)
	}

	select {
	case e := <-events:
		if e.Kind != "power_button_shutdown" {
			t.Fatalf("expected power_button_shutdown, got %s", e.Kind)
		}
	default:
		t.Fatal("expected an event to be published")
	}
}

func TestSurveyPublishesErrorEventOnCommandFailure(t *testing.T) {
	h, runner, bus := newHandler()
	runner.err = errors.New("systemctl failed")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := bus.Subscribe(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := h.Survey(context.Background(), true, true); err == nil {
		t.Fatalf("expected Survey to propagate the command error")
	}
	select {
	case event := <-ch:
		if event.Severity != "error" || event.Kind != "survey_start_error" {
			t.Fatalf("unexpected event: %+v", event)
		}
	default:
		t.Fatalf("expected an error event to be published")
	}
}
