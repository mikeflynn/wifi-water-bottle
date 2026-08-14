// Package host is the real, OS-backed implementation of lifecycle.Host. It
// only runs meaningfully on the Raspberry Pi target; every external touch
// point (shell commands, filesystem roots, network addresses) is injectable
// so it can be unit tested on any platform without root or real hardware.
package host

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// CommandRunner executes a shell command line. Production uses sh -c;
// tests inject a fake to assert which commands get built without running
// anything.
type CommandRunner interface {
	Run(ctx context.Context, command string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, command string) ([]byte, error) {
	return exec.CommandContext(ctx, "sh", "-c", command).CombinedOutput()
}

// Host is the real lifecycle.Host implementation.
type Host struct {
	runner CommandRunner

	osReleasePath string
	netClassRoot  string
	statfsPath    string
	gpsdAddr      string
	kismetAddr    string
	fetchMaxBytes int64

	backupDir   string
	releasesDir string
	currentLink string

	serviceNames []string
}

type Option func(*Host)

func WithCommandRunner(r CommandRunner) Option { return func(h *Host) { h.runner = r } }
func WithOSReleasePath(p string) Option        { return func(h *Host) { h.osReleasePath = p } }
func WithNetClassRoot(p string) Option         { return func(h *Host) { h.netClassRoot = p } }
func WithStatfsPath(p string) Option           { return func(h *Host) { h.statfsPath = p } }
func WithGPSDAddr(addr string) Option          { return func(h *Host) { h.gpsdAddr = addr } }
func WithKismetAddr(addr string) Option        { return func(h *Host) { h.kismetAddr = addr } }
func WithBackupDir(p string) Option            { return func(h *Host) { h.backupDir = p } }
func WithReleasesDir(p string) Option          { return func(h *Host) { h.releasesDir = p } }
func WithCurrentLink(p string) Option          { return func(h *Host) { h.currentLink = p } }
func WithServiceNames(names ...string) Option  { return func(h *Host) { h.serviceNames = names } }

// New builds a Host wired to real Raspberry Pi OS paths; override individual
// paths/dependencies with Options in tests.
func New(opts ...Option) *Host {
	h := &Host{
		runner:        execRunner{},
		osReleasePath: "/etc/os-release",
		netClassRoot:  "/sys/class/net",
		statfsPath:    "/",
		gpsdAddr:      "127.0.0.1:2947",
		kismetAddr:    "127.0.0.1:2501",
		fetchMaxBytes: 512 << 20,
		backupDir:     "/var/lib/bottle-agent/backups",
		releasesDir:   "/opt/bottle-agent/releases",
		currentLink:   "/opt/bottle-agent/current",
		serviceNames:  []string{"bottle-agent", "kismet"},
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

func (h *Host) Architecture() string { return runtime.GOARCH }

func (h *Host) IsBookworm() bool {
	data, err := os.ReadFile(h.osReleasePath)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "VERSION_CODENAME="); ok {
			return strings.Trim(strings.TrimSpace(value), `"`) == "bookworm"
		}
	}
	return false
}

func (h *Host) FreeStorageMB() int {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(h.statfsPath, &stat); err != nil {
		return 0
	}
	return int(int64(stat.Bavail) * int64(stat.Bsize) / (1024 * 1024))
}

func (h *Host) HasEthernet(name string) bool {
	_, err := net.InterfaceByName(name)
	return err == nil
}

func (h *Host) RadioCount() int {
	matches, err := filepath.Glob(filepath.Join(h.netClassRoot, "*", "wireless"))
	if err != nil {
		return 0
	}
	return len(matches)
}

func (h *Host) GPSVisible() bool {
	conn, err := net.DialTimeout("tcp", h.gpsdAddr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (h *Host) Config(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func (h *Host) Backup(paths []string) error {
	if err := os.MkdirAll(h.backupDir, 0o700); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	name := fmt.Sprintf("backup-%s.tar.gz", time.Now().UTC().Format("20060102T150405Z"))
	dest := filepath.Join(h.backupDir, name)
	file, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create backup archive: %w", err)
	}
	defer file.Close()

	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	for _, path := range paths {
		if err := addToTar(tw, path); err != nil {
			return fmt.Errorf("backup %s: %w", path, err)
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return file.Sync()
}

func addToTar(tw *tar.Writer, root string) error {
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil // nothing to preserve yet; not an error
	} else if err != nil {
		return err
	}
	return filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		header.Name = path
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		content, err := os.Open(path)
		if err != nil {
			return err
		}
		defer content.Close()
		_, err = io.Copy(tw, content)
		return err
	})
}

func (h *Host) Run(ctx context.Context, command string) error {
	output, err := h.runner.Run(ctx, command)
	if err != nil {
		return fmt.Errorf("run %q: %w: %s", command, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (h *Host) HealthCheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, service := range h.serviceNames {
		output, err := h.runner.Run(ctx, "systemctl is-active "+service)
		if err != nil || strings.TrimSpace(string(output)) != "active" {
			return fmt.Errorf("service %s is not active: %s", service, strings.TrimSpace(string(output)))
		}
	}
	conn, err := net.DialTimeout("tcp", h.kismetAddr, 2*time.Second)
	if err != nil {
		return fmt.Errorf("kismet not reachable at %s: %w", h.kismetAddr, err)
	}
	return conn.Close()
}

func (h *Host) Fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, h.fetchMaxBytes))
}

func (h *Host) Stage(version string, payload []byte) error {
	dir := filepath.Join(h.releasesDir, version)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create release dir: %w", err)
	}
	dest := filepath.Join(dir, "release.bin")
	if err := os.WriteFile(dest, payload, 0o600); err != nil {
		return fmt.Errorf("write staged release: %w", err)
	}
	return nil
}

func (h *Host) ActiveRelease() string {
	target, err := os.Readlink(h.currentLink)
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

func (h *Host) Activate(version string) error { return h.switchTo(version) }
func (h *Host) Rollback(version string) error { return h.switchTo(version) }

// switchTo atomically repoints the "current" symlink at the given release
// directory and restarts bottle-agent. Activate and Rollback are the same
// mechanical operation; the distinction is which caller (updater vs. failed
// health check) invokes it.
func (h *Host) switchTo(version string) error {
	target := filepath.Join(h.releasesDir, version)
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("release %s is not staged: %w", version, err)
	}
	tmp := h.currentLink + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return fmt.Errorf("stage current symlink: %w", err)
	}
	if err := os.Rename(tmp, h.currentLink); err != nil {
		return fmt.Errorf("activate current symlink: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := h.runner.Run(ctx, "systemctl restart bottle-agent"); err != nil {
		return fmt.Errorf("restart bottle-agent: %w", err)
	}
	return nil
}
