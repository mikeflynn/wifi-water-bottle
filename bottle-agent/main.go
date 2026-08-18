package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/agent"
	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/controlplane"
	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/eventbus"
	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/host"
	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/lifecycle"
	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/pki"
	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/tunnel"
)

const (
	kismetAddr = "127.0.0.1:2501"

	caValidity     = 10 * 365 * 24 * time.Hour
	serverValidity = 10 * 365 * 24 * time.Hour
	clientValidity = 5 * 365 * 24 * time.Hour
)

// paths locates every file bottle-agent reads or writes. defaultPaths()
// points at the real production locations; tests construct a paths value
// pointing at a t.TempDir() instead, so the setup/run wiring logic is
// exercised for real without root or touching a real Pi's filesystem.
type paths struct {
	pkiDir   string
	stateDir string
}

func defaultPaths() paths {
	return paths{
		pkiDir:   envOr("BOTTLE_AGENT_PKI_DIR", "/etc/bottle-agent/pki"),
		stateDir: envOr("BOTTLE_AGENT_STATE_DIR", "/var/lib/bottle-agent"),
	}
}

func (p paths) caCertPath() string   { return filepath.Join(p.pkiDir, "ca.pem") }
func (p paths) caKeyPath() string    { return filepath.Join(p.pkiDir, "ca-key.pem") }
func (p paths) serverCert() string   { return filepath.Join(p.pkiDir, "server-cert.pem") }
func (p paths) serverKey() string    { return filepath.Join(p.pkiDir, "server-key.pem") }
func (p paths) pairingsPath() string { return filepath.Join(p.stateDir, "pairings.json") }
func (p paths) jobsPath() string     { return filepath.Join(p.stateDir, "jobs.json") }

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

type gpioConfig struct {
	chip       string
	buttonPin  int
	buttonHold time.Duration
	ledPin     int
}

func loadGPIOConfig() (gpioConfig, error) {
	buttonPin, err := strconv.Atoi(envOr("BOTTLE_AGENT_BUTTON_PIN", "17"))
	if err != nil {
		return gpioConfig{}, fmt.Errorf("parse BOTTLE_AGENT_BUTTON_PIN: %w", err)
	}
	ledPin, err := strconv.Atoi(envOr("BOTTLE_AGENT_LED_PIN", "27"))
	if err != nil {
		return gpioConfig{}, fmt.Errorf("parse BOTTLE_AGENT_LED_PIN: %w", err)
	}
	hold, err := time.ParseDuration(envOr("BOTTLE_AGENT_BUTTON_HOLD", "2s"))
	if err != nil {
		return gpioConfig{}, fmt.Errorf("parse BOTTLE_AGENT_BUTTON_HOLD: %w", err)
	}
	return gpioConfig{chip: "gpiochip0", buttonPin: buttonPin, buttonHold: hold, ledPin: ledPin}, nil
}

func main() {
	args := os.Args[1:]
	var err error
	if len(args) > 0 && args[0] == "setup" {
		err = runSetupCommand(args[1:], os.Stdout)
	} else {
		err = runServiceCommand(args)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func requireRoot() error {
	if os.Geteuid() != 0 {
		return errors.New("must run as root (writes under /etc/bottle-agent and /var/lib/bottle-agent)")
	}
	return nil
}

// runServiceCommand starts the mTLS control plane and Kismet tunnel relay.
// It expects `bottle-agent setup` to have already been run once.
func runServiceCommand(args []string) error {
	if err := requireRoot(); err != nil {
		return err
	}
	if len(args) != 0 {
		return errors.New("usage: bottle-agent [setup --profile NAME]")
	}
	return runService(defaultPaths(), os.Stdout)
}

func runService(p paths, out io.Writer) error {
	server, err := startService(p)
	if err != nil {
		return err
	}
	defer server.Close()

	fmt.Fprintf(out, "bottle-agent listening at %s (Kismet relay -> %s)\n", controlplane.ListenAddress, kismetAddr)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	<-ctx.Done()
	fmt.Fprintln(out, "shutting down")
	return nil
}

// startService performs all the wiring — loading PKI/pairing/job state,
// constructing the handler, and starting the mTLS listener and Kismet relay
// — without blocking, so tests can start it, assert it worked, and Close it
// immediately instead of waiting on an OS signal.
func startService(p paths) (*controlplane.Server, error) {
	caPEM, err := os.ReadFile(p.caCertPath())
	if err != nil {
		return nil, fmt.Errorf("read CA (run `bottle-agent setup` first): %w", err)
	}
	roots, err := controlplane.PinnedClientCA(caPEM)
	if err != nil {
		return nil, err
	}
	cert, err := tls.LoadX509KeyPair(p.serverCert(), p.serverKey())
	if err != nil {
		return nil, fmt.Errorf("load server certificate (run `bottle-agent setup` first): %w", err)
	}
	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    roots,
		Certificates: []tls.Certificate{cert},
	}

	pairings, err := controlplane.NewPersistentPairingStore(p.pairingsPath())
	if err != nil {
		return nil, fmt.Errorf("load pairing store: %w", err)
	}

	h := host.New()
	jobs, err := lifecycle.NewFileJobs(p.jobsPath())
	if err != nil {
		return nil, fmt.Errorf("load job store: %w", err)
	}
	provisioner := lifecycle.NewProvisioner(h, jobs)
	bus := eventbus.New(0)
	handler := agent.New(provisioner, h, bus)

	server, err := controlplane.Listen(controlplane.ListenAddress, tlsConfig, pairings, handler)
	if err != nil {
		return nil, fmt.Errorf("start control plane: %w", err)
	}

	relay, err := tunnel.NewServer(kismetAddr, net.Dial)
	if err != nil {
		server.Close()
		return nil, fmt.Errorf("start Kismet tunnel relay: %w", err)
	}
	server.SetKismetTunnel(relay)

	return server, nil
}

func runSetupCommand(args []string, out io.Writer) error {
	if err := requireRoot(); err != nil {
		return err
	}
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	profile := fs.String("profile", "", "name for this laptop profile, e.g. laptop-profile")
	outDir := fs.String("out", "bottle-agent-profiles", "directory (relative to cwd) to write the profile's PEM files into")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *profile == "" {
		return errors.New("setup requires --profile NAME")
	}
	_, err := doSetup(defaultPaths(), *profile, *outDir, out)
	return err
}

// doSetup is the one-time local bootstrap: generate (or reuse) the CA and
// Pi server certificate, issue a new client certificate for the named
// profile, and pair its fingerprint directly into the persistent allowlist
// — no network round-trip, so nothing needs to be exposed for it. It
// returns the new profile's fingerprint for callers (and tests) that want
// to confirm what got paired.
func doSetup(p paths, profile, outDir string, out io.Writer) (fingerprint string, err error) {
	ca, err := loadOrCreateCA(p)
	if err != nil {
		return "", err
	}
	if err := ensureServerCert(p, ca); err != nil {
		return "", err
	}

	clientCertPEM, clientKeyPEM, err := ca.IssueClientCert(profile, clientValidity)
	if err != nil {
		return "", fmt.Errorf("issue client certificate: %w", err)
	}
	fingerprint, err = pki.Fingerprint(clientCertPEM)
	if err != nil {
		return "", err
	}

	pairings, err := controlplane.NewPersistentPairingStore(p.pairingsPath())
	if err != nil {
		return "", fmt.Errorf("load pairing store: %w", err)
	}
	pairings.OpenPhysicalWindow()
	if err := pairings.Pair(fingerprint); err != nil {
		pairings.ClosePhysicalWindow()
		return "", fmt.Errorf("pair profile: %w", err)
	}
	pairings.ClosePhysicalWindow()

	caCertPEM, _, err := ca.EncodePEM()
	if err != nil {
		return "", err
	}
	profileDir := filepath.Join(outDir, profile)
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return "", fmt.Errorf("create profile output dir: %w", err)
	}
	files := map[string][]byte{
		"ca.pem":          caCertPEM,
		"client-cert.pem": clientCertPEM,
		"client-key.pem":  clientKeyPEM,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(profileDir, name), data, 0o600); err != nil {
			return "", fmt.Errorf("write %s: %w", name, err)
		}
	}

	fmt.Fprintf(out, "Profile %q paired (fingerprint %s).\n", profile, fingerprint)
	fmt.Fprintf(out, "Copy these to your laptop, then run:\n\n")
	fmt.Fprintf(out, "  scp -r %s <laptop>:~/\n\n", profileDir)
	fmt.Fprintf(out, "  cd bottle-tui\n")
	fmt.Fprintf(out, "  go run . control profile import --ca %s/ca.pem --cert %s/client-cert.pem --key %s/client-key.pem --id %s\n",
		profile, profile, profile, profile)
	fmt.Fprintf(out, "\nThese files are owned by root; `sudo scp`/`sudo chown` them off as needed.\n")
	return fingerprint, nil
}

func loadOrCreateCA(p paths) (*pki.CA, error) {
	certPEM, certErr := os.ReadFile(p.caCertPath())
	keyPEM, keyErr := os.ReadFile(p.caKeyPath())
	if certErr == nil && keyErr == nil {
		return pki.LoadCA(certPEM, keyPEM)
	}
	ca, err := pki.NewCA("bottle-agent", caValidity)
	if err != nil {
		return nil, fmt.Errorf("generate CA: %w", err)
	}
	certPEM, keyPEM, err = ca.EncodePEM()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(p.pkiDir, 0o700); err != nil {
		return nil, fmt.Errorf("create PKI dir: %w", err)
	}
	if err := os.WriteFile(p.caCertPath(), certPEM, 0o600); err != nil {
		return nil, fmt.Errorf("write CA certificate: %w", err)
	}
	if err := os.WriteFile(p.caKeyPath(), keyPEM, 0o600); err != nil {
		return nil, fmt.Errorf("write CA key: %w", err)
	}
	return ca, nil
}

func ensureServerCert(p paths, ca *pki.CA) error {
	if _, err := os.Stat(p.serverCert()); err == nil {
		return nil
	}
	certPEM, keyPEM, err := ca.IssueServerCert("bottle-agent", []net.IP{net.ParseIP("10.77.0.1")}, serverValidity)
	if err != nil {
		return fmt.Errorf("issue server certificate: %w", err)
	}
	if err := os.WriteFile(p.serverCert(), certPEM, 0o600); err != nil {
		return fmt.Errorf("write server certificate: %w", err)
	}
	if err := os.WriteFile(p.serverKey(), keyPEM, 0o600); err != nil {
		return fmt.Errorf("write server key: %w", err)
	}
	return nil
}
