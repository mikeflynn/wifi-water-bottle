package host

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	commands []string
	output   map[string][]byte
	err      error
}

func (r *fakeRunner) Run(_ context.Context, command string) ([]byte, error) {
	r.commands = append(r.commands, command)
	if r.err != nil {
		return nil, r.err
	}
	if out, ok := r.output[command]; ok {
		return out, nil
	}
	return nil, nil
}

func TestIsBookwormParsesOSRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "os-release")
	if err := os.WriteFile(path, []byte("PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\nVERSION_CODENAME=bookworm\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := New(WithOSReleasePath(path))
	if !h.IsBookworm() {
		t.Fatalf("expected bookworm to be detected")
	}
}

func TestIsBookwormFalseForOtherRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "os-release")
	if err := os.WriteFile(path, []byte("VERSION_CODENAME=bullseye\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := New(WithOSReleasePath(path))
	if h.IsBookworm() {
		t.Fatalf("expected non-bookworm release to be rejected")
	}
}

func TestRadioCountCountsWirelessInterfaces(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "wlan0", "wireless"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "eth0"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := New(WithNetClassRoot(root))
	if got := h.RadioCount(); got != 1 {
		t.Fatalf("RadioCount() = %d, want 1", got)
	}
}

func TestGPSVisibleDialsConfiguredAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	h := New(WithGPSDAddr(ln.Addr().String()))
	if !h.GPSVisible() {
		t.Fatalf("expected GPSVisible to succeed against a live listener")
	}
	h2 := New(WithGPSDAddr("127.0.0.1:1"))
	if h2.GPSVisible() {
		t.Fatalf("expected GPSVisible to fail against a closed port")
	}
}

func TestConfigReadsExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kismet.conf")
	if err := os.WriteFile(path, []byte("user-managed=true"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := New()
	content, ok := h.Config(path)
	if !ok || content != "user-managed=true" {
		t.Fatalf("Config() = %q, %v", content, ok)
	}
	if _, ok := h.Config(filepath.Join(t.TempDir(), "missing.conf")); ok {
		t.Fatalf("expected missing file to report not-exists")
	}
}

func TestBackupArchivesExistingPathsAndSkipsMissing(t *testing.T) {
	src := t.TempDir()
	present := filepath.Join(src, "config.yaml")
	if err := os.WriteFile(present, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	backupDir := t.TempDir()
	h := New(WithBackupDir(backupDir))
	if err := h.Backup([]string{present, filepath.Join(src, "missing.yaml")}); err != nil {
		t.Fatalf("Backup() error = %v", err)
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected exactly one backup archive, got %v (err=%v)", entries, err)
	}
}

func TestRunWrapsCommandFailureWithOutput(t *testing.T) {
	runner := &fakeRunner{err: context.DeadlineExceeded, output: map[string][]byte{}}
	h := New(WithCommandRunner(runner))
	err := h.Run(context.Background(), "apt-get install --yes kismet gpsd nftables")
	if err == nil || !strings.Contains(err.Error(), "apt-get install") {
		t.Fatalf("Run() error = %v", err)
	}
	if len(runner.commands) != 1 || runner.commands[0] != "apt-get install --yes kismet gpsd nftables" {
		t.Fatalf("unexpected commands: %#v", runner.commands)
	}
}

func TestHealthCheckRequiresActiveServicesAndReachableKismet(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	runner := &fakeRunner{output: map[string][]byte{
		"systemctl is-active bottle-agent": []byte("active\n"),
		"systemctl is-active kismet":       []byte("active\n"),
	}}
	h := New(WithCommandRunner(runner), WithKismetAddr(ln.Addr().String()))
	if err := h.HealthCheck(); err != nil {
		t.Fatalf("HealthCheck() error = %v", err)
	}

	runner2 := &fakeRunner{output: map[string][]byte{
		"systemctl is-active bottle-agent": []byte("active\n"),
		"systemctl is-active kismet":       []byte("failed\n"),
	}}
	h2 := New(WithCommandRunner(runner2), WithKismetAddr(ln.Addr().String()))
	if err := h2.HealthCheck(); err == nil {
		t.Fatalf("expected HealthCheck to fail when kismet service is not active")
	}
}

func TestStageActivateActiveReleaseRoundTrip(t *testing.T) {
	releasesDir := t.TempDir()
	currentLink := filepath.Join(t.TempDir(), "current")
	runner := &fakeRunner{}
	h := New(WithCommandRunner(runner), WithReleasesDir(releasesDir), WithCurrentLink(currentLink))

	if err := h.Stage("v2", []byte("release-bytes")); err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	if err := h.Activate("v2"); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if got := h.ActiveRelease(); got != "v2" {
		t.Fatalf("ActiveRelease() = %q, want v2", got)
	}
	found := false
	for _, cmd := range runner.commands {
		if cmd == "systemctl restart bottle-agent" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Activate to restart bottle-agent, got commands %#v", runner.commands)
	}

	if err := h.Activate("v3"); err == nil {
		t.Fatalf("expected Activate to fail for an unstaged release")
	}
	if got := h.ActiveRelease(); got != "v2" {
		t.Fatalf("ActiveRelease() after failed activate = %q, want v2 (unchanged)", got)
	}
}
