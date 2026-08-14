// Package lifecycle provides the durable, idempotent provisioning and release-update
// workflow used by bottle-agent. Privileged commands are deliberately represented by
// narrow host operations; the laptop never receives arbitrary shell access.
package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

type State string

const (
	Queued     State = "queued"
	Running    State = "running"
	Succeeded  State = "succeeded"
	Failed     State = "failed"
	NeedsInput State = "needs_input"
)

var (
	ErrDigestMismatch   = errors.New("release digest mismatch")
	ErrUpdateRolledBack = errors.New("update rolled back after failed health check")
)

type Job struct {
	ID           string
	Kind         string
	State        State
	Phase        string
	Message      string
	ChangedPaths []string
	Completed    map[string]bool
	UpdatedAt    time.Time
}

type Jobs interface {
	Get(id string) (Job, bool)
	Put(Job)
}

type MemoryJobs struct {
	mu   sync.Mutex
	jobs map[string]Job
}

func NewMemoryJobs() *MemoryJobs { return &MemoryJobs{jobs: map[string]Job{}} }
func (m *MemoryJobs) Get(id string) (Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	return cloneJob(j), ok
}
func (m *MemoryJobs) Put(j Job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j.UpdatedAt = time.Now().UTC()
	m.jobs[j.ID] = cloneJob(j)
}
func cloneJob(j Job) Job {
	j.ChangedPaths = append([]string(nil), j.ChangedPaths...)
	completed := j.Completed
	j.Completed = map[string]bool{}
	for k, v := range completed {
		j.Completed[k] = v
	}
	return j
}

type Host interface {
	Architecture() string
	IsBookworm() bool
	FreeStorageMB() int
	HasEthernet(string) bool
	RadioCount() int
	GPSVisible() bool
	Config(string) (string, bool)
	Backup([]string) error
	Run(context.Context, string) error
	HealthCheck() error
	Fetch(context.Context, string) ([]byte, error)
	Stage(string, []byte) error
	ActiveRelease() string
	Activate(string) error
	Rollback(string) error
}

type ProvisionRequest struct {
	ID                   string
	ConfirmConfigChanges bool
}

type Provisioner struct {
	host Host
	jobs Jobs
}

func NewProvisioner(host Host, jobs Jobs) *Provisioner { return &Provisioner{host: host, jobs: jobs} }

var protectedConfigPaths = []string{"/etc/kismet/kismet.conf", "/etc/bottle-agent/config.yaml"}

func (p *Provisioner) Run(ctx context.Context, req ProvisionRequest) (Job, error) {
	if req.ID == "" {
		return Job{}, errors.New("provision request id is required")
	}
	job, ok := p.jobs.Get(req.ID)
	if !ok {
		job = Job{ID: req.ID, Kind: "provision", State: Queued, Completed: map[string]bool{}}
	}
	if job.State == Succeeded {
		return job, nil
	}
	if err := p.preflight(); err != nil {
		job.State, job.Phase, job.Message = Failed, "preflight", err.Error()
		p.jobs.Put(job)
		return job, nil
	}
	if !job.Completed["backup"] {
		var changed []string
		for _, path := range protectedConfigPaths {
			if _, exists := p.host.Config(path); exists {
				changed = append(changed, path)
			}
		}
		if len(changed) > 0 && !req.ConfirmConfigChanges {
			job.State, job.Phase, job.Message, job.ChangedPaths = NeedsInput, "confirm_config", "existing configuration will be backed up; explicit confirmation required", changed
			p.jobs.Put(job)
			return job, nil
		}
		if err := p.host.Backup(append([]string(nil), protectedConfigPaths...)); err != nil {
			return p.fail(job, "backup", err)
		}
		job.Completed["backup"] = true
	}
	steps := []struct{ checkpoint, command string }{
		{"packages", "apt-get install --yes kismet gpsd nftables"},
		{"configure", "bottle-provisioner configure --preserve-existing --backup-created"},
		{"services", "systemctl enable --now bottle-agent kismet"},
	}
	for _, step := range steps {
		if job.Completed[step.checkpoint] {
			continue
		}
		job.State, job.Phase = Running, step.checkpoint
		p.jobs.Put(job)
		if err := p.host.Run(ctx, step.command); err != nil {
			return p.fail(job, step.checkpoint, err)
		}
		job.Completed[step.checkpoint] = true
	}
	if err := p.host.HealthCheck(); err != nil {
		return p.fail(job, "health", err)
	}
	job.Completed["health"], job.State, job.Phase, job.Message = true, Succeeded, "complete", "provisioning completed; mTLS, loopback Kismet, radios, GPS, and service health verified"
	p.jobs.Put(job)
	return job, nil
}

func (p *Provisioner) preflight() error {
	if p.host.Architecture() != "arm64" {
		return fmt.Errorf("unsupported architecture %q: Raspberry Pi arm64 required", p.host.Architecture())
	}
	if !p.host.IsBookworm() {
		return errors.New("Raspberry Pi OS Bookworm is required")
	}
	if p.host.FreeStorageMB() < 2048 {
		return errors.New("at least 2048 MB free storage is required")
	}
	if !p.host.HasEthernet("eth0") {
		return errors.New("required management interface eth0 is unavailable")
	}
	if p.host.RadioCount() == 0 {
		return errors.New("no supported radio is visible")
	}
	if !p.host.GPSVisible() {
		return errors.New("GPS is not visible")
	}
	return nil
}
func (p *Provisioner) fail(job Job, phase string, err error) (Job, error) {
	job.State, job.Phase, job.Message = Failed, phase, err.Error()
	p.jobs.Put(job)
	return job, nil
}

type ReleaseManifest struct{ Version, URL, SHA256 string }
type UpdateRequest struct {
	ID       string
	Manifest ReleaseManifest
}
type ManifestVerifier func(ReleaseManifest) error

type Updater struct {
	host   Host
	jobs   Jobs
	verify ManifestVerifier
}

func NewUpdater(host Host, jobs Jobs, verify ManifestVerifier) *Updater {
	return &Updater{host: host, jobs: jobs, verify: verify}
}

func (u *Updater) Run(ctx context.Context, req UpdateRequest) (Job, error) {
	if req.ID == "" || req.Manifest.Version == "" || req.Manifest.URL == "" || req.Manifest.SHA256 == "" {
		return Job{}, errors.New("update request requires id and complete manifest")
	}
	if prior, ok := u.jobs.Get(req.ID); ok && prior.State == Succeeded {
		return prior, nil
	}
	job := Job{ID: req.ID, Kind: "update", State: Running, Completed: map[string]bool{}}
	u.jobs.Put(job)
	if u.verify == nil {
		return u.fail(job, "verify_manifest", errors.New("no signed manifest verifier configured"))
	}
	if err := u.verify(req.Manifest); err != nil {
		return u.fail(job, "verify_manifest", err)
	}
	payload, err := u.host.Fetch(ctx, req.Manifest.URL)
	if err != nil {
		return u.fail(job, "download", err)
	}
	if SHA256(payload) != req.Manifest.SHA256 {
		return u.fail(job, "verify_digest", ErrDigestMismatch)
	}
	preserved := []string{"/var/lib/bottle-agent", "/var/lib/bottle-agent/exports", "/var/log/bottle-agent", "/etc/bottle-agent/config.yaml"}
	if err := u.host.Backup(preserved); err != nil {
		return u.fail(job, "backup", err)
	}
	previous := u.host.ActiveRelease()
	if err := u.host.Stage(req.Manifest.Version, payload); err != nil {
		return u.fail(job, "stage", err)
	}
	if err := u.host.Activate(req.Manifest.Version); err != nil {
		return u.fail(job, "activate", err)
	}
	if err := u.host.HealthCheck(); err != nil {
		if rollbackErr := u.host.Rollback(previous); rollbackErr != nil {
			return u.fail(job, "rollback", fmt.Errorf("health check failed: %w; rollback failed: %v", err, rollbackErr))
		}
		job.State, job.Phase, job.Message = Failed, "rolled_back", "UPDATE_ROLLED_BACK: staged release failed health checks; prior release restored"
		u.jobs.Put(job)
		return job, ErrUpdateRolledBack
	}
	job.State, job.Phase, job.Message = Succeeded, "complete", "update activated after signed-manifest, digest, and health checks"
	u.jobs.Put(job)
	return job, nil
}
func (u *Updater) fail(job Job, phase string, err error) (Job, error) {
	job.State, job.Phase, job.Message = Failed, phase, err.Error()
	u.jobs.Put(job)
	return job, err
}
func SHA256(payload []byte) string { sum := sha256.Sum256(payload); return hex.EncodeToString(sum[:]) }
