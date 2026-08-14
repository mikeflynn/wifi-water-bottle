package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeHost struct {
	arch       string
	bookworm   bool
	storageMB  int
	ethernet   bool
	radios     int
	gps        bool
	configs    map[string]string
	backups    []string
	commands   []string
	healthErr  error
	payload    []byte
	staged     []string
	active     string
	activated  []string
	rolledBack int
}

func (h *fakeHost) Architecture() string              { return h.arch }
func (h *fakeHost) IsBookworm() bool                  { return h.bookworm }
func (h *fakeHost) FreeStorageMB() int                { return h.storageMB }
func (h *fakeHost) HasEthernet(name string) bool      { return h.ethernet && name == "eth0" }
func (h *fakeHost) RadioCount() int                   { return h.radios }
func (h *fakeHost) GPSVisible() bool                  { return h.gps }
func (h *fakeHost) Config(path string) (string, bool) { v, ok := h.configs[path]; return v, ok }
func (h *fakeHost) Backup(paths []string) error       { h.backups = append(h.backups, paths...); return nil }
func (h *fakeHost) Run(_ context.Context, command string) error {
	h.commands = append(h.commands, command)
	return nil
}
func (h *fakeHost) HealthCheck() error                                { return h.healthErr }
func (h *fakeHost) Fetch(_ context.Context, _ string) ([]byte, error) { return h.payload, nil }
func (h *fakeHost) Stage(version string, _ []byte) error {
	h.staged = append(h.staged, version)
	return nil
}
func (h *fakeHost) ActiveRelease() string { return h.active }
func (h *fakeHost) Activate(version string) error {
	h.activated = append(h.activated, version)
	h.active = version
	return nil
}
func (h *fakeHost) Rollback(version string) error { h.rolledBack++; h.active = version; return nil }

func newHost() *fakeHost {
	return &fakeHost{arch: "arm64", bookworm: true, storageMB: 4096, ethernet: true, radios: 1, gps: true, configs: map[string]string{}}
}

func TestProvisionRequiresConfirmationForExistingUserConfig(t *testing.T) {
	host := newHost()
	host.configs["/etc/kismet/kismet.conf"] = "user-managed=true"
	jobs := NewMemoryJobs()
	workflow := NewProvisioner(host, jobs)

	job, err := workflow.Run(context.Background(), ProvisionRequest{ID: "provision-1"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if job.State != NeedsInput {
		t.Fatalf("state = %s, want %s", job.State, NeedsInput)
	}
	if len(job.ChangedPaths) != 1 || job.ChangedPaths[0] != "/etc/kismet/kismet.conf" {
		t.Fatalf("changed paths = %#v", job.ChangedPaths)
	}
	if len(host.commands) != 0 {
		t.Fatalf("commands executed without confirmation: %#v", host.commands)
	}
}

func TestProvisionIsResumableAndIdempotent(t *testing.T) {
	host := newHost()
	jobs := NewMemoryJobs()
	workflow := NewProvisioner(host, jobs)
	req := ProvisionRequest{ID: "provision-2", ConfirmConfigChanges: true}

	first, err := workflow.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if first.State != Succeeded {
		t.Fatalf("first state = %s", first.State)
	}
	commandCount := len(host.commands)
	second, err := workflow.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if second.State != Succeeded {
		t.Fatalf("second state = %s", second.State)
	}
	if len(host.commands) != commandCount {
		t.Fatalf("idempotent retry ran commands: %d -> %d", commandCount, len(host.commands))
	}
}

func TestProvisionRejectsUnsupportedHardware(t *testing.T) {
	host := newHost()
	host.arch = "amd64"
	workflow := NewProvisioner(host, NewMemoryJobs())
	job, err := workflow.Run(context.Background(), ProvisionRequest{ID: "provision-3", ConfirmConfigChanges: true})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if job.State != Failed || !strings.Contains(job.Message, "arm64") {
		t.Fatalf("job = %#v", job)
	}
}

func TestUpdateRejectsDigestMismatchBeforeStaging(t *testing.T) {
	host := newHost()
	host.payload = []byte("tampered release")
	workflow := NewUpdater(host, NewMemoryJobs(), func(ReleaseManifest) error { return nil })
	_, err := workflow.Run(context.Background(), UpdateRequest{ID: "update-1", Manifest: ReleaseManifest{Version: "v2", URL: "https://updates.example/v2", SHA256: strings.Repeat("0", 64)}})
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Run() error = %v, want ErrDigestMismatch", err)
	}
	if len(host.staged) != 0 {
		t.Fatalf("staged a corrupt release: %#v", host.staged)
	}
}

func TestUpdateRollsBackWhenHealthCheckFails(t *testing.T) {
	host := newHost()
	host.active = "v1"
	host.payload = []byte("release-v2")
	host.healthErr = errors.New("agent unavailable")
	workflow := NewUpdater(host, NewMemoryJobs(), func(ReleaseManifest) error { return nil })
	_, err := workflow.Run(context.Background(), UpdateRequest{ID: "update-2", Manifest: ReleaseManifest{Version: "v2", URL: "https://updates.example/v2", SHA256: SHA256(host.payload)}})
	if !errors.Is(err, ErrUpdateRolledBack) {
		t.Fatalf("Run() error = %v", err)
	}
	if host.active != "v1" || host.rolledBack != 1 {
		t.Fatalf("rollback = active %q, calls %d", host.active, host.rolledBack)
	}
}

func TestUpdatePreservesStateBeforeActivation(t *testing.T) {
	host := newHost()
	host.active = "v1"
	host.payload = []byte("release-v2")
	workflow := NewUpdater(host, NewMemoryJobs(), func(ReleaseManifest) error { return nil })
	job, err := workflow.Run(context.Background(), UpdateRequest{ID: "update-3", Manifest: ReleaseManifest{Version: "v2", URL: "https://updates.example/v2", SHA256: SHA256(host.payload)}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if job.State != Succeeded {
		t.Fatalf("state = %s", job.State)
	}
	joined := strings.Join(host.backups, ",")
	for _, path := range []string{"/var/lib/bottle-agent", "/var/log/bottle-agent", "/etc/bottle-agent/config.yaml"} {
		if !strings.Contains(joined, path) {
			t.Errorf("backup paths = %q, missing %s", joined, path)
		}
	}
}
