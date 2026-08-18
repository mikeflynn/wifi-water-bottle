package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/controlplane"
	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/pki"
)

func testPaths(t *testing.T) paths {
	t.Helper()
	root := t.TempDir()
	return paths{
		pkiDir:   filepath.Join(root, "pki"),
		stateDir: filepath.Join(root, "state"),
	}
}

func TestDoSetupCreatesCAServerCertAndPairsProfile(t *testing.T) {
	p := testPaths(t)
	outDir := filepath.Join(t.TempDir(), "profiles")
	var out bytes.Buffer

	fingerprint, err := doSetup(p, "laptop-profile", outDir, &out)
	if err != nil {
		t.Fatalf("doSetup() error = %v", err)
	}

	for _, f := range []string{p.caCertPath(), p.caKeyPath(), p.serverCert(), p.serverKey()} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("expected PKI file %s to exist: %v", f, err)
		}
	}

	profileDir := filepath.Join(outDir, "laptop-profile")
	clientCertPEM, err := os.ReadFile(filepath.Join(profileDir, "client-cert.pem"))
	if err != nil {
		t.Fatalf("read issued client cert: %v", err)
	}
	if _, err := os.ReadFile(filepath.Join(profileDir, "client-key.pem")); err != nil {
		t.Fatalf("read issued client key: %v", err)
	}
	if _, err := os.ReadFile(filepath.Join(profileDir, "ca.pem")); err != nil {
		t.Fatalf("read CA copy: %v", err)
	}

	want, err := pki.Fingerprint(clientCertPEM)
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint != want {
		t.Fatalf("returned fingerprint %s does not match issued cert's fingerprint %s", fingerprint, want)
	}

	pairings, err := controlplane.NewPersistentPairingStore(p.pairingsPath())
	if err != nil {
		t.Fatalf("reload pairing store: %v", err)
	}
	if !pairings.Allowed(fingerprint) {
		t.Fatalf("expected fingerprint %s to be paired", fingerprint)
	}

	if !strings.Contains(out.String(), "control profile import") {
		t.Errorf("expected setup output to include the laptop import command, got: %s", out.String())
	}
}

func TestDoSetupReusesExistingCAForAdditionalProfiles(t *testing.T) {
	p := testPaths(t)
	outDir := filepath.Join(t.TempDir(), "profiles")
	var out bytes.Buffer

	firstFingerprint, err := doSetup(p, "laptop-1", outDir, &out)
	if err != nil {
		t.Fatalf("first doSetup() error = %v", err)
	}
	caBefore, err := os.ReadFile(p.caCertPath())
	if err != nil {
		t.Fatal(err)
	}

	secondFingerprint, err := doSetup(p, "laptop-2", outDir, &out)
	if err != nil {
		t.Fatalf("second doSetup() error = %v", err)
	}
	caAfter, err := os.ReadFile(p.caCertPath())
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(caBefore, caAfter) {
		t.Fatalf("expected CA to be reused across setup runs, but it changed")
	}
	if firstFingerprint == secondFingerprint {
		t.Fatalf("expected distinct fingerprints for distinct profiles")
	}

	pairings, err := controlplane.NewPersistentPairingStore(p.pairingsPath())
	if err != nil {
		t.Fatal(err)
	}
	if !pairings.Allowed(firstFingerprint) || !pairings.Allowed(secondFingerprint) {
		t.Fatalf("expected both profiles to remain paired")
	}
}

func TestStartServiceFailsClearlyBeforeSetupHasRun(t *testing.T) {
	p := testPaths(t)
	if _, err := startService(context.Background(), p); err == nil || !strings.Contains(err.Error(), "bottle-agent setup") {
		t.Fatalf("expected a clear \"run setup first\" error, got %v", err)
	}
}

// TestStartServiceLoadsValidPKI proves the PKI/pairing/job wiring path
// itself is correct: after doSetup, startService gets far enough to attempt
// the real mTLS listener bind (which is pinned to 10.77.0.1:7443 and will
// fail in this sandbox — there is no such interface here — but would
// succeed on a real Pi with the static IP configured, per docs/pi-setup.md).
// A failure anywhere before that point would indicate a real wiring bug.
func TestStartServiceLoadsValidPKI(t *testing.T) {
	p := testPaths(t)
	var out bytes.Buffer
	if _, err := doSetup(p, "laptop-profile", filepath.Join(t.TempDir(), "profiles"), &out); err != nil {
		t.Fatalf("doSetup() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // stops the gpsd fix-watcher goroutine wireGPIO starts

	_, err := startService(ctx, p)
	if err == nil {
		t.Fatalf("expected startService to fail in this sandbox (no 10.77.0.1 interface), but it succeeded")
	}
	if !strings.Contains(err.Error(), "start control plane") {
		t.Fatalf("expected the failure to be at the listener bind step, got: %v", err)
	}

	// Independently confirm the certs startService loaded are actually
	// valid and chain to each other, using the exact same stdlib calls
	// startService uses.
	caPEM, err := os.ReadFile(p.caCertPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controlplane.PinnedClientCA(caPEM); err != nil {
		t.Fatalf("PinnedClientCA() error = %v", err)
	}
	if _, err := tls.LoadX509KeyPair(p.serverCert(), p.serverKey()); err != nil {
		t.Fatalf("server cert/key is not a valid TLS key pair: %v", err)
	}
}

func TestRunServiceCommandRejectsExtraArgs(t *testing.T) {
	if err := runServiceCommand([]string{"unexpected"}); err == nil {
		t.Fatalf("expected an error for unexpected arguments")
	}
}

func TestLoadGPIOConfigDefaults(t *testing.T) {
	cfg, warnings := loadGPIOConfig()
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if cfg.buttonPin != 17 || cfg.ledPin != 27 || cfg.buttonHold != 2*time.Second {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadGPIOConfigReadsEnvOverrides(t *testing.T) {
	t.Setenv("BOTTLE_AGENT_BUTTON_PIN", "5")
	t.Setenv("BOTTLE_AGENT_LED_PIN", "6")
	t.Setenv("BOTTLE_AGENT_BUTTON_HOLD", "500ms")

	cfg, warnings := loadGPIOConfig()
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if cfg.buttonPin != 5 || cfg.ledPin != 6 || cfg.buttonHold != 500*time.Millisecond {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

// TestLoadGPIOConfigFallsBackToDefaultOnInvalidPin proves a single malformed
// env var falls back to its default rather than making loadGPIOConfig fail
// entirely — a config typo must never prevent bottle-agent's control plane
// and tunnel from starting (see startService's use of loadGPIOConfig).
func TestLoadGPIOConfigFallsBackToDefaultOnInvalidPin(t *testing.T) {
	t.Setenv("BOTTLE_AGENT_BUTTON_PIN", "not-a-number")
	t.Setenv("BOTTLE_AGENT_LED_PIN", "9")

	cfg, warnings := loadGPIOConfig()
	if cfg.buttonPin != 17 {
		t.Fatalf("expected buttonPin to fall back to default 17, got %d", cfg.buttonPin)
	}
	if cfg.ledPin != 9 {
		t.Fatalf("expected valid ledPin override to still be honored, got %d", cfg.ledPin)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "BOTTLE_AGENT_BUTTON_PIN") {
		t.Fatalf("expected exactly one warning mentioning BOTTLE_AGENT_BUTTON_PIN, got %v", warnings)
	}
}

// TestLoadGPIOConfigFallsBackToDefaultOnInvalidHold covers the duration
// field specifically, since it uses a different parser (time.ParseDuration)
// than the two pin fields (strconv.Atoi).
func TestLoadGPIOConfigFallsBackToDefaultOnInvalidHold(t *testing.T) {
	t.Setenv("BOTTLE_AGENT_BUTTON_HOLD", "not-a-duration")

	cfg, warnings := loadGPIOConfig()
	if cfg.buttonHold != 2*time.Second {
		t.Fatalf("expected buttonHold to fall back to default 2s, got %v", cfg.buttonHold)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "BOTTLE_AGENT_BUTTON_HOLD") {
		t.Fatalf("expected exactly one warning mentioning BOTTLE_AGENT_BUTTON_HOLD, got %v", warnings)
	}
}
