# GPIO Power Button + GPS-Lock LED Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a GPIO power button (hold-to-shutdown) and a GPS-lock status LED to `bottle-agent`, and surface GPS fix state on the `bottle-tui` dashboard.

**Architecture:** Two new `bottle-agent` packages — `internal/gpio` (button/LED components built on small `InputLine`/`OutputLine` interfaces, real implementations backed by `go-gpiocdev`) and `internal/gpsd` (a persistent gpsd JSON-protocol client that watches `TPV` reports for fix state). Both are wired into `main.go`'s `startService`, feeding into new `agent.Handler` methods (`SetGPSFix`, `Shutdown`) that update `controlplane.Status` and publish bus events. `bottle-tui` threads the new `Status.GPSFix` field through to the dashboard the same way `Survey` already flows.

**Tech Stack:** Go, `github.com/warthog618/go-gpiocdev` (GPIO chardev ABI), gpsd JSON protocol over TCP (127.0.0.1:2947).

## Global Constraints

- `bottle-agent` module: `github.com/mikeflynn/wifi-water-bottle/bottle-agent`, Go 1.24.0.
- `bottle-tui` module: `github.com/mikeflynn/wifi-water-bottle/bottle-tui`, Go 1.26.3.
- Follow the existing "thin hardware shim + testable core" pattern (`internal/host.Host`): hardware/network touch points sit behind small interfaces; business logic is unit tested against fakes, not real hardware.
- GPIO/gpsd absence must never prevent `bottle-agent`'s control plane or tunnel from starting — log and continue, don't fail `startService`.
- Config values follow the existing `envOr("BOTTLE_AGENT_*", default)` pattern already used in `main.go`.
- Env vars (from the design spec): `BOTTLE_AGENT_BUTTON_PIN` (default `17`), `BOTTLE_AGENT_BUTTON_HOLD` (default `2s`), `BOTTLE_AGENT_LED_PIN` (default `27`). GPIO chip defaults to `gpiochip0`. gpsd address reuses the existing default `127.0.0.1:2947`.
- Design doc: `docs/superpowers/specs/2026-08-17-gpio-power-button-gps-led-design.md`.

---

## Task 1: `internal/gpio` — Button and Led components

**Files:**
- Create: `bottle-agent/internal/gpio/interfaces.go`
- Create: `bottle-agent/internal/gpio/button.go`
- Create: `bottle-agent/internal/gpio/button_test.go`
- Create: `bottle-agent/internal/gpio/led.go`
- Create: `bottle-agent/internal/gpio/led_test.go`

**Interfaces:**
- Produces: `gpio.InputLine` (`WatchEdges(ctx context.Context, fn func(active bool)) error`), `gpio.OutputLine` (`Set(active bool) error`), `gpio.NewButton(line InputLine, hold time.Duration, onHeld func()) *Button`, `(*Button).Run(ctx context.Context) error`, `gpio.NewLed(line OutputLine) *Led`, `(*Led).Set(active bool) error` — Task 2 (real hardware impl) and Task 5 (main.go wiring) depend on these exact names/signatures.

- [ ] **Step 1: Create the package and its hardware interfaces**

```go
// bottle-agent/internal/gpio/interfaces.go

// Package gpio provides hardware-independent power-button and status-LED
// components on top of small interfaces, so their timing/business logic is
// unit-testable without real GPIO hardware. Real, chip-backed
// implementations of these interfaces live in chip.go (added in a later
// task).
package gpio

import "context"

// InputLine watches a single GPIO line for level changes.
type InputLine interface {
	// WatchEdges blocks, invoking fn(active) on every level change, until
	// ctx is done or the underlying line fails.
	WatchEdges(ctx context.Context, fn func(active bool)) error
}

// OutputLine drives a single GPIO line high or low.
type OutputLine interface {
	Set(active bool) error
}
```

- [ ] **Step 2: Write the failing Button tests**

```go
// bottle-agent/internal/gpio/button_test.go
package gpio

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// fakeInputLine lets tests drive simulated button presses/releases. ready
// is closed once WatchEdges has registered fn, so tests don't race the
// goroutine running Run.
type fakeInputLine struct {
	ready chan struct{}
	fn    func(active bool)
}

func newFakeInputLine() *fakeInputLine {
	return &fakeInputLine{ready: make(chan struct{})}
}

func (f *fakeInputLine) WatchEdges(ctx context.Context, fn func(active bool)) error {
	f.fn = fn
	close(f.ready)
	<-ctx.Done()
	return nil
}

func (f *fakeInputLine) press()   { f.fn(true) }
func (f *fakeInputLine) release() { f.fn(false) }

func TestButtonFiresAfterHeldPastDuration(t *testing.T) {
	line := newFakeInputLine()
	var fired int32
	b := NewButton(line, 30*time.Millisecond, func() { atomic.AddInt32(&fired, 1) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	<-line.ready

	line.press()
	time.Sleep(60 * time.Millisecond)

	if got := atomic.LoadInt32(&fired); got != 1 {
		t.Fatalf("expected onHeld to fire once, got %d", got)
	}
}

func TestButtonDoesNotFireOnShortPress(t *testing.T) {
	line := newFakeInputLine()
	var fired int32
	b := NewButton(line, 50*time.Millisecond, func() { atomic.AddInt32(&fired, 1) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	<-line.ready

	line.press()
	time.Sleep(10 * time.Millisecond)
	line.release()
	time.Sleep(60 * time.Millisecond)

	if got := atomic.LoadInt32(&fired); got != 0 {
		t.Fatalf("expected onHeld not to fire on short press, got %d", got)
	}
}

func TestButtonFiresAgainAfterReleaseAndReHold(t *testing.T) {
	line := newFakeInputLine()
	var fired int32
	b := NewButton(line, 20*time.Millisecond, func() { atomic.AddInt32(&fired, 1) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	<-line.ready

	line.press()
	time.Sleep(40 * time.Millisecond)
	line.release()
	time.Sleep(10 * time.Millisecond)
	line.press()
	time.Sleep(40 * time.Millisecond)

	if got := atomic.LoadInt32(&fired); got != 2 {
		t.Fatalf("expected onHeld to fire twice, got %d", got)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd bottle-agent && go test ./internal/gpio/... -run TestButton -v`
Expected: FAIL — `NewButton` undefined (button.go doesn't exist yet).

- [ ] **Step 4: Implement Button**

```go
// bottle-agent/internal/gpio/button.go
package gpio

import (
	"context"
	"sync"
	"time"
)

// Button watches an InputLine and calls onHeld once the line has been
// continuously active for at least hold. A press released before hold
// elapses does not fire; holding again after a release fires again.
type Button struct {
	line   InputLine
	hold   time.Duration
	onHeld func()
}

func NewButton(line InputLine, hold time.Duration, onHeld func()) *Button {
	return &Button{line: line, hold: hold, onHeld: onHeld}
}

// Run blocks until ctx is done or the underlying line fails.
func (b *Button) Run(ctx context.Context) error {
	var mu sync.Mutex
	var timer *time.Timer
	fired := false

	return b.line.WatchEdges(ctx, func(active bool) {
		mu.Lock()
		defer mu.Unlock()
		if active {
			fired = false
			timer = time.AfterFunc(b.hold, func() {
				mu.Lock()
				already := fired
				fired = true
				mu.Unlock()
				if !already {
					b.onHeld()
				}
			})
			return
		}
		if timer != nil {
			timer.Stop()
			timer = nil
		}
	})
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd bottle-agent && go test ./internal/gpio/... -run TestButton -v`
Expected: PASS (all three tests).

- [ ] **Step 6: Write the failing Led test**

```go
// bottle-agent/internal/gpio/led_test.go
package gpio

import "testing"

type fakeOutputLine struct {
	values []bool
	err    error
}

func (f *fakeOutputLine) Set(active bool) error {
	f.values = append(f.values, active)
	return f.err
}

func TestLedSetPassesThroughToLine(t *testing.T) {
	line := &fakeOutputLine{}
	led := NewLed(line)

	if err := led.Set(true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := led.Set(false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(line.values) != 2 || line.values[0] != true || line.values[1] != false {
		t.Fatalf("expected [true false], got %v", line.values)
	}
}
```

- [ ] **Step 7: Run the test to verify it fails**

Run: `cd bottle-agent && go test ./internal/gpio/... -run TestLed -v`
Expected: FAIL — `NewLed` undefined.

- [ ] **Step 8: Implement Led**

```go
// bottle-agent/internal/gpio/led.go
package gpio

// Led drives a status LED via an OutputLine.
type Led struct {
	line OutputLine
}

func NewLed(line OutputLine) *Led {
	return &Led{line: line}
}

func (l *Led) Set(active bool) error {
	return l.line.Set(active)
}
```

- [ ] **Step 9: Run all gpio tests to verify they pass**

Run: `cd bottle-agent && go test ./internal/gpio/... -v`
Expected: PASS (all tests).

- [ ] **Step 10: Commit**

```bash
cd bottle-agent
git add internal/gpio/interfaces.go internal/gpio/button.go internal/gpio/button_test.go internal/gpio/led.go internal/gpio/led_test.go
git commit -m "Add gpio.Button and gpio.Led on top of testable line interfaces"
```

---

## Task 2: `internal/gpio` — real chip-backed line implementations

**Files:**
- Modify: `bottle-agent/go.mod`, `bottle-agent/go.sum` (add `github.com/warthog618/go-gpiocdev`)
- Create: `bottle-agent/internal/gpio/chip.go`

**Interfaces:**
- Consumes: `gpio.InputLine`, `gpio.OutputLine` from Task 1.
- Produces: `gpio.NewChipInputLine(chip string, offset int) (InputLine, error)`, `gpio.NewChipOutputLine(chip string, offset int) (OutputLine, error)` — Task 5 (main.go wiring) depends on these exact names/signatures.

This file talks to real GPIO hardware via `go-gpiocdev`'s chardev ioctl ABI. It cannot be unit tested without a Raspberry Pi (`/dev/gpiochip0` doesn't exist elsewhere) — verify it manually per the Manual Verification section at the end of this plan, once Task 5 wires it up. This task's steps are build-and-commit, not TDD.

- [ ] **Step 1: Add the go-gpiocdev dependency**

Run:
```bash
cd bottle-agent
go get github.com/warthog618/go-gpiocdev@latest
```

- [ ] **Step 2: Implement the real line types**

```go
// bottle-agent/internal/gpio/chip.go
package gpio

import (
	"context"
	"fmt"
	"time"

	"github.com/warthog618/go-gpiocdev"
)

// chipInputLine is the real, hardware-backed InputLine.
type chipInputLine struct {
	chip   string
	offset int
}

// NewChipInputLine probes that chip:offset can be requested as an input
// (failing fast if the chip or line doesn't exist), then returns an
// InputLine that opens it for real, with edge detection, inside
// WatchEdges.
func NewChipInputLine(chip string, offset int) (InputLine, error) {
	probe, err := gpiocdev.RequestLine(chip, offset, gpiocdev.AsInput)
	if err != nil {
		return nil, fmt.Errorf("open input line %s:%d: %w", chip, offset, err)
	}
	_ = probe.Close()
	return &chipInputLine{chip: chip, offset: offset}, nil
}

func (c *chipInputLine) WatchEdges(ctx context.Context, fn func(active bool)) error {
	handler := func(evt gpiocdev.LineEvent) {
		fn(evt.Type == gpiocdev.LineEventRisingEdge)
	}
	line, err := gpiocdev.RequestLine(c.chip, c.offset,
		gpiocdev.AsInput,
		gpiocdev.WithPullUp,
		gpiocdev.WithBothEdges,
		gpiocdev.WithDebounce(10*time.Millisecond),
		gpiocdev.WithEventHandler(handler),
	)
	if err != nil {
		return fmt.Errorf("watch input line %s:%d: %w", c.chip, c.offset, err)
	}
	defer line.Close()
	<-ctx.Done()
	return nil
}

// chipOutputLine is the real, hardware-backed OutputLine.
type chipOutputLine struct {
	line *gpiocdev.Line
}

// NewChipOutputLine requests chip:offset as an output, initially low.
func NewChipOutputLine(chip string, offset int) (OutputLine, error) {
	line, err := gpiocdev.RequestLine(chip, offset, gpiocdev.AsOutput(0))
	if err != nil {
		return nil, fmt.Errorf("open output line %s:%d: %w", chip, offset, err)
	}
	return &chipOutputLine{line: line}, nil
}

func (o *chipOutputLine) Set(active bool) error {
	v := 0
	if active {
		v = 1
	}
	return o.line.SetValue(v)
}
```

- [ ] **Step 3: Verify the module builds**

Run: `cd bottle-agent && go build ./...`
Expected: succeeds with no errors. If `go-gpiocdev`'s actual option/type names differ slightly from above (check with `go doc github.com/warthog618/go-gpiocdev`), adjust `chip.go` to match — the compiler will point at exactly which identifiers are wrong.

- [ ] **Step 4: Commit**

```bash
cd bottle-agent
git add go.mod go.sum internal/gpio/chip.go
git commit -m "Add go-gpiocdev-backed real GPIO line implementations"
```

---

## Task 3: `internal/gpsd` — FixWatcher

**Files:**
- Create: `bottle-agent/internal/gpsd/fixwatcher.go`
- Create: `bottle-agent/internal/gpsd/fixwatcher_test.go`

**Interfaces:**
- Produces: `gpsd.NewFixWatcher(addr string, onChange func(fix bool), opts ...gpsd.Option) *FixWatcher`, `(*FixWatcher).Run(ctx context.Context) error`, `gpsd.WithBackoff(func(attempt int) time.Duration) Option` — Task 5 (main.go wiring) depends on these exact names/signatures.

- [ ] **Step 1: Write the failing tests**

```go
// bottle-agent/internal/gpsd/fixwatcher_test.go
package gpsd

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"
)

func startFakeGPSD(t *testing.T, respond func(conn net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		respond(conn)
	}()
	return ln.Addr().String()
}

func TestFixWatcherReportsFixOnTPVModeThreeOrGreater(t *testing.T) {
	addr := startFakeGPSD(t, func(conn net.Conn) {
		defer conn.Close()
		bufio.NewReader(conn).ReadString('\n') // consume the WATCH command
		conn.Write([]byte(`{"class":"TPV","mode":3}` + "\n"))
		time.Sleep(200 * time.Millisecond)
	})

	fixes := make(chan bool, 4)
	w := NewFixWatcher(addr, func(fix bool) { fixes <- fix })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	select {
	case fix := <-fixes:
		if !fix {
			t.Fatalf("expected first fix report to be true")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fix report")
	}
}

func TestFixWatcherSkipsMalformedLines(t *testing.T) {
	addr := startFakeGPSD(t, func(conn net.Conn) {
		defer conn.Close()
		bufio.NewReader(conn).ReadString('\n')
		conn.Write([]byte("not json\n"))
		conn.Write([]byte(`{"class":"TPV","mode":2}` + "\n"))
		time.Sleep(200 * time.Millisecond)
	})

	fixes := make(chan bool, 4)
	w := NewFixWatcher(addr, func(fix bool) { fixes <- fix })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	select {
	case fix := <-fixes:
		if !fix {
			t.Fatalf("expected the valid TPV report to be delivered")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fix report")
	}
}

func TestFixWatcherReportsNoFixAfterDisconnectThenReconnects(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	connCount := 0
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			connCount++
			n := connCount
			go func() {
				defer conn.Close()
				bufio.NewReader(conn).ReadString('\n')
				conn.Write([]byte(`{"class":"TPV","mode":3}` + "\n"))
				if n == 1 {
					time.Sleep(20 * time.Millisecond)
					return // close after first report: simulates a dropped connection
				}
				time.Sleep(200 * time.Millisecond)
			}()
		}
	}()

	var fixes []bool
	fixCh := make(chan bool, 8)
	w := NewFixWatcher(ln.Addr().String(), func(fix bool) { fixCh <- fix },
		WithBackoff(func(int) time.Duration { return time.Millisecond }))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	deadline := time.After(2 * time.Second)
	for len(fixes) < 3 {
		select {
		case fix := <-fixCh:
			fixes = append(fixes, fix)
		case <-deadline:
			t.Fatalf("timed out waiting for reconnect sequence, got %v", fixes)
		}
	}

	if fixes[0] != true || fixes[1] != false || fixes[2] != true {
		t.Fatalf("expected [true false true] (fix, disconnect, reconnect-fix), got %v", fixes)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd bottle-agent && go test ./internal/gpsd/... -v`
Expected: FAIL — package `gpsd` / `NewFixWatcher` doesn't exist yet.

- [ ] **Step 3: Implement FixWatcher**

```go
// bottle-agent/internal/gpsd/fixwatcher.go

// Package gpsd is a minimal client for gpsd's JSON protocol, used to watch
// GPS fix status for driving a status LED and the agent's Status.GPSFix
// field.
package gpsd

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"time"
)

const watchCommand = `?WATCH={"enable":true,"json":true}` + "\n"

// FixWatcher connects to gpsd, watches TPV reports, and calls onChange
// whenever the fix state (mode >= 2, i.e. 2D or 3D fix) changes. It
// reconnects with backoff on any connection error and reports "no fix"
// while disconnected.
type FixWatcher struct {
	addr     string
	onChange func(fix bool)
	backoff  func(attempt int) time.Duration
}

type Option func(*FixWatcher)

// WithBackoff overrides the reconnect backoff schedule (default:
// exponential from 1s, capped at 30s). Tests use this to avoid real
// sleeps.
func WithBackoff(b func(attempt int) time.Duration) Option {
	return func(w *FixWatcher) { w.backoff = b }
}

func NewFixWatcher(addr string, onChange func(fix bool), opts ...Option) *FixWatcher {
	w := &FixWatcher{addr: addr, onChange: onChange, backoff: defaultBackoff}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

func defaultBackoff(attempt int) time.Duration {
	d := time.Second << attempt
	if d <= 0 || d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

// Run blocks, reconnecting to gpsd as needed, until ctx is done.
func (w *FixWatcher) Run(ctx context.Context) error {
	lastFix := false
	first := true
	notify := func(fix bool) {
		if first || fix != lastFix {
			lastFix = fix
			first = false
			w.onChange(fix)
		}
	}

	attempt := 0
	for ctx.Err() == nil {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", w.addr)
		if err != nil {
			notify(false)
			if !sleepCtx(ctx, w.backoff(attempt)) {
				return ctx.Err()
			}
			attempt++
			continue
		}

		attempt = 0
		streamErr := w.streamFixes(ctx, conn, notify)
		conn.Close()
		notify(false)
		if streamErr != nil && ctx.Err() == nil {
			if !sleepCtx(ctx, w.backoff(attempt)) {
				return ctx.Err()
			}
			attempt++
		}
	}
	return ctx.Err()
}

func (w *FixWatcher) streamFixes(ctx context.Context, conn net.Conn, notify func(bool)) error {
	if _, err := conn.Write([]byte(watchCommand)); err != nil {
		return err
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var report struct {
			Class string `json:"class"`
			Mode  int    `json:"mode"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &report); err != nil {
			continue
		}
		if report.Class == "TPV" {
			notify(report.Mode >= 2)
		}
	}
	return scanner.Err()
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd bottle-agent && go test ./internal/gpsd/... -v`
Expected: PASS (all three tests).

- [ ] **Step 5: Commit**

```bash
cd bottle-agent
git add internal/gpsd/fixwatcher.go internal/gpsd/fixwatcher_test.go
git commit -m "Add gpsd.FixWatcher for TPV-based satellite fix detection"
```

---

## Task 4: `agent.Handler` — GPS fix state, shutdown, and Status.GPSFix

**Files:**
- Modify: `bottle-agent/internal/controlplane/server.go:38-42` (Status struct)
- Modify: `bottle-agent/internal/agent/agent.go:32-50` (Handler struct, Status method)
- Modify: `bottle-agent/internal/agent/agent.go` (add SetGPSFix, Shutdown methods after Survey, currently ending at line 93)
- Modify: `bottle-agent/internal/agent/agent_test.go` (add `time` import, add new tests)

**Interfaces:**
- Consumes: `eventbus.Bus.Publish(severity, source, kind string, payload map[string]any) controlplane.Event` (existing), `Runner.Run(ctx, command) error` (existing).
- Produces: `(*Handler).SetGPSFix(fix bool)`, `(*Handler).Shutdown(ctx context.Context) error`, `controlplane.Status.GPSFix bool` — Task 5 (main.go wiring) and Task 6 (bottle-tui) depend on these exact names.

- [ ] **Step 1: Add GPSFix to controlplane.Status**

In `bottle-agent/internal/controlplane/server.go`, change:

```go
type Status struct {
	Ready   bool   `json:"ready"`
	Survey  string `json:"survey"`
	Message string `json:"message"`
}
```

to:

```go
type Status struct {
	Ready   bool   `json:"ready"`
	Survey  string `json:"survey"`
	GPSFix  bool   `json:"gps_fix"`
	Message string `json:"message"`
}
```

- [ ] **Step 2: Write the failing agent tests**

Add to `bottle-agent/internal/agent/agent_test.go` (add `"time"` to the existing import block):

```go
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
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd bottle-agent && go test ./internal/agent/... -run 'TestSetGPSFix|TestShutdown' -v`
Expected: FAIL — `h.SetGPSFix` / `h.Shutdown` undefined, and `status.GPSFix` doesn't compile.

- [ ] **Step 4: Implement the Handler changes**

In `bottle-agent/internal/agent/agent.go`, change the `Handler` struct:

```go
type Handler struct {
	provisioner *lifecycle.Provisioner
	runner      Runner
	bus         *eventbus.Bus

	mu          sync.Mutex
	surveyState string
	gpsFix      bool
}
```

Change `Status`:

```go
func (h *Handler) Status(context.Context) (controlplane.Status, error) {
	h.mu.Lock()
	survey := h.surveyState
	gpsFix := h.gpsFix
	h.mu.Unlock()
	return controlplane.Status{Ready: true, Survey: survey, GPSFix: gpsFix, Message: "bottle-agent running"}, nil
}
```

Add these two methods after `Survey` (which currently ends at line 93, right before `Events`):

```go
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
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd bottle-agent && go test ./internal/agent/... ./internal/controlplane/... -v`
Expected: PASS. (Running `controlplane` too confirms the `Status` struct change didn't break existing tests — `fakeHandler.Status` already returns a zero-value `Status{}`, so `GPSFix` defaulting to `false` is transparent.)

- [ ] **Step 6: Commit**

```bash
cd bottle-agent
git add internal/controlplane/server.go internal/agent/agent.go internal/agent/agent_test.go
git commit -m "Add GPS fix tracking and button-triggered shutdown to agent.Handler"
```

---

## Task 5: `main.go` — wire GPIO/gpsd into the running service

**Files:**
- Modify: `bottle-agent/main.go` (imports, `runService`, `startService` signature and body, new `gpioConfig`/`loadGPIOConfig`/`wireGPIO`)
- Modify: `bottle-agent/main_test.go` (imports, two `startService` call sites, new config tests)

**Interfaces:**
- Consumes: `gpio.NewChipInputLine`, `gpio.NewChipOutputLine`, `gpio.NewButton`, `gpio.NewLed` (Tasks 1-2); `gpsd.NewFixWatcher`, `gpsd.WithBackoff` (Task 3); `(*agent.Handler).SetGPSFix`, `(*agent.Handler).Shutdown` (Task 4).
- Produces: `loadGPIOConfig() (gpioConfig, error)`, `wireGPIO(ctx context.Context, cfg gpioConfig, handler *agent.Handler)`, `startService(ctx context.Context, p paths) (*controlplane.Server, error)` (signature change — was `startService(p paths)`).

- [ ] **Step 1: Write the failing config-parsing tests**

Add to `bottle-agent/main_test.go`:

```go
func TestLoadGPIOConfigDefaults(t *testing.T) {
	cfg, err := loadGPIOConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.buttonPin != 17 || cfg.ledPin != 27 || cfg.buttonHold != 2*time.Second {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadGPIOConfigReadsEnvOverrides(t *testing.T) {
	t.Setenv("BOTTLE_AGENT_BUTTON_PIN", "5")
	t.Setenv("BOTTLE_AGENT_LED_PIN", "6")
	t.Setenv("BOTTLE_AGENT_BUTTON_HOLD", "500ms")

	cfg, err := loadGPIOConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.buttonPin != 5 || cfg.ledPin != 6 || cfg.buttonHold != 500*time.Millisecond {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadGPIOConfigRejectsInvalidPin(t *testing.T) {
	t.Setenv("BOTTLE_AGENT_BUTTON_PIN", "not-a-number")
	if _, err := loadGPIOConfig(); err == nil {
		t.Fatal("expected an error for an invalid button pin")
	}
}
```

Add `"time"` to the existing import block in `main_test.go` if not already present, and add `"context"` (needed for Step 6 below).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd bottle-agent && go test . -run TestLoadGPIOConfig -v`
Expected: FAIL — `loadGPIOConfig` undefined.

- [ ] **Step 3: Add gpioConfig and loadGPIOConfig to main.go**

Add near the top of `bottle-agent/main.go`, after the `paths` type and its methods (after line 62's `envOr`):

```go
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
```

Add `"strconv"` to `main.go`'s import block.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd bottle-agent && go test . -run TestLoadGPIOConfig -v`
Expected: PASS (all three tests).

- [ ] **Step 5: Commit the config step**

```bash
cd bottle-agent
git add main.go main_test.go
git commit -m "Add loadGPIOConfig for button/LED pin and hold-duration env vars"
```

- [ ] **Step 6: Thread a context into startService and add wireGPIO**

In `bottle-agent/main.go`, change `runService`:

```go
func runService(p paths, out io.Writer) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	server, err := startService(ctx, p)
	if err != nil {
		return err
	}
	defer server.Close()

	fmt.Fprintf(out, "bottle-agent listening at %s (Kismet relay -> %s)\n", controlplane.ListenAddress, kismetAddr)

	<-ctx.Done()
	fmt.Fprintln(out, "shutting down")
	return nil
}
```

Change `startService`'s signature and add the wiring call right after `handler := agent.New(provisioner, h, bus)`:

```go
func startService(ctx context.Context, p paths) (*controlplane.Server, error) {
	// ... unchanged CA/cert/pairing/host/jobs/provisioner/bus/handler setup ...

	handler := agent.New(provisioner, h, bus)

	gpioCfg, err := loadGPIOConfig()
	if err != nil {
		return nil, fmt.Errorf("load gpio config: %w", err)
	}
	wireGPIO(ctx, gpioCfg, handler)

	server, err := controlplane.Listen(controlplane.ListenAddress, tlsConfig, pairings, handler)
	// ... unchanged from here ...
}
```

Add `wireGPIO` at the end of `main.go`:

```go
// wireGPIO starts the power-button and GPS-lock-LED background watchers.
// Missing GPIO hardware (e.g. running off the Pi) is logged and skipped,
// not fatal — the rest of the agent must still start. GPS fix state is
// still tracked via handler.SetGPSFix even if the LED itself is
// unavailable, so Status/events stay accurate.
func wireGPIO(ctx context.Context, cfg gpioConfig, handler *agent.Handler) {
	buttonLine, err := gpio.NewChipInputLine(cfg.chip, cfg.buttonPin)
	if err != nil {
		log.Printf("gpio: power button unavailable: %v", err)
	} else {
		button := gpio.NewButton(buttonLine, cfg.buttonHold, func() {
			if err := handler.Shutdown(context.Background()); err != nil {
				log.Printf("gpio: shutdown command failed: %v", err)
			}
		})
		go func() {
			if err := button.Run(ctx); err != nil {
				log.Printf("gpio: power button watcher stopped: %v", err)
			}
		}()
	}

	setLED := func(bool) error { return nil }
	ledLine, err := gpio.NewChipOutputLine(cfg.chip, cfg.ledPin)
	if err != nil {
		log.Printf("gpio: GPS-lock LED unavailable: %v", err)
	} else {
		led := gpio.NewLed(ledLine)
		setLED = led.Set
	}

	watcher := gpsd.NewFixWatcher("127.0.0.1:2947", func(fix bool) {
		if err := setLED(fix); err != nil {
			log.Printf("gpio: set LED failed: %v", err)
		}
		handler.SetGPSFix(fix)
	})
	go func() {
		if err := watcher.Run(ctx); err != nil {
			log.Printf("gpsd: fix watcher stopped: %v", err)
		}
	}()
}
```

Add `"log"` to `main.go`'s import block, plus:
```go
"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/gpio"
"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/gpsd"
```

- [ ] **Step 7: Update the two existing startService call sites in main_test.go**

In `bottle-agent/main_test.go`:

```go
func TestStartServiceFailsClearlyBeforeSetupHasRun(t *testing.T) {
	p := testPaths(t)
	if _, err := startService(context.Background(), p); err == nil || !strings.Contains(err.Error(), "bottle-agent setup") {
		t.Fatalf("expected a clear \"run setup first\" error, got %v", err)
	}
}
```

```go
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

	// ... rest unchanged ...
}
```

- [ ] **Step 8: Run the full bottle-agent test suite**

Run: `cd bottle-agent && go build ./... && go vet ./... && go test ./... -v`
Expected: PASS across all packages. `TestStartServiceFailsClearlyBeforeSetupHasRun` fails before reaching `wireGPIO` (no CA yet), so it's unaffected. `TestStartServiceLoadsValidPKI` reaches `wireGPIO` (GPIO hardware absent in the test sandbox, logged and skipped) and still fails at the listener-bind step as before.

- [ ] **Step 9: Commit**

```bash
cd bottle-agent
git add main.go main_test.go
git commit -m "Wire GPIO power button and GPS-lock LED into startService"
```

---

## Task 6: `bottle-tui` — surface GPSFix on the dashboard

**Files:**
- Modify: `bottle-tui/internal/controlplane/client.go:36-41` (Status struct)
- Modify: `bottle-tui/internal/model/events.go:48-52` (StatusSnapshot struct)
- Modify: `bottle-tui/internal/controlplane/eventstream.go:57-67` (FetchStatus)
- Modify: `bottle-tui/internal/tui/dashboard.go:121-127` (render)
- Modify: `bottle-tui/internal/tui/dashboard_test.go` (new tests)

**Interfaces:**
- Consumes: `controlplane.Status.GPSFix bool` (Task 4, mirrored on the `bottle-agent` side — the two `Status` structs are independent types connected only by the `gps_fix` JSON tag, same as `Ready`/`Survey`/`Message` already are).

- [ ] **Step 1: Write the failing dashboard tests**

Add to `bottle-tui/internal/tui/dashboard_test.go`:

```go
func TestDashboardStatusResultRendersGPSFix(t *testing.T) {
	m := newDashboardModel(testEngine())
	updated, _ := m.Update(statusResultMsg{status: controlplane.Status{Ready: true, Survey: "idle", GPSFix: true}})
	updated.haveFetch = true
	updated.paired = true
	view := updated.View()
	if !strings.Contains(view, "locked") {
		t.Fatalf("expected gps locked state in view: %s", view)
	}
}

func TestDashboardStatusResultRendersNoGPSFix(t *testing.T) {
	m := newDashboardModel(testEngine())
	updated, _ := m.Update(statusResultMsg{status: controlplane.Status{Ready: true, Survey: "idle", GPSFix: false}})
	updated.haveFetch = true
	updated.paired = true
	view := updated.View()
	if !strings.Contains(view, "no fix") {
		t.Fatalf("expected no-fix state in view: %s", view)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd bottle-tui && go test ./internal/tui/... -run TestDashboardStatusResultRendersGPSFix -v`
Expected: FAIL — `controlplane.Status` has no `GPSFix` field yet (compile error).

- [ ] **Step 3: Add GPSFix to controlplane.Status (bottle-tui side)**

In `bottle-tui/internal/controlplane/client.go`, change:

```go
type Status struct {
	Ready   bool   `json:"ready"`
	Survey  string `json:"survey"`
	Message string `json:"message"`
}
```

to:

```go
type Status struct {
	Ready   bool   `json:"ready"`
	Survey  string `json:"survey"`
	GPSFix  bool   `json:"gps_fix"`
	Message string `json:"message"`
}
```

- [ ] **Step 4: Add GPSFix to StatusSnapshot and thread it through FetchStatus**

In `bottle-tui/internal/model/events.go`, change:

```go
type StatusSnapshot struct {
	Survey     string    `json:"survey"`
	ObservedAt time.Time `json:"observed_at"`
	Stale      bool      `json:"stale"`
}
```

to:

```go
type StatusSnapshot struct {
	Survey     string    `json:"survey"`
	GPSFix     bool      `json:"gps_fix"`
	ObservedAt time.Time `json:"observed_at"`
	Stale      bool      `json:"stale"`
}
```

In `bottle-tui/internal/controlplane/eventstream.go`, change `FetchStatus`:

```go
func FetchStatus(c *Client) model.FetchStatus {
	return func(ctx context.Context) (model.StatusSnapshot, error) {
		status, err := c.Status(ctx)
		if err != nil {
			return model.StatusSnapshot{}, err
		}
		return model.StatusSnapshot{Survey: status.Survey, GPSFix: status.GPSFix, ObservedAt: time.Now().UTC()}, nil
	}
}
```

- [ ] **Step 5: Render the GPS line on the dashboard**

In `bottle-tui/internal/tui/dashboard.go`, change:

```go
out := styleLabel.Render("pi:      ") + controlplane.PiAddress + "\n"
out += styleLabel.Render("state:   ") + readyLine + "\n"
out += styleLabel.Render("survey:  ") + m.status.Survey + "\n"
if m.status.Message != "" {
	out += styleLabel.Render("message: ") + m.status.Message + "\n"
}
```

to:

```go
out := styleLabel.Render("pi:      ") + controlplane.PiAddress + "\n"
out += styleLabel.Render("state:   ") + readyLine + "\n"
out += styleLabel.Render("survey:  ") + m.status.Survey + "\n"
gpsLine := styleDim.Render("no fix")
if m.status.GPSFix {
	gpsLine = styleSuccess.Render("locked")
}
out += styleLabel.Render("gps:     ") + gpsLine + "\n"
if m.status.Message != "" {
	out += styleLabel.Render("message: ") + m.status.Message + "\n"
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd bottle-tui && go test ./... -v`
Expected: PASS across all packages, including the two new dashboard tests and the existing `TestDashboardStatusResultRendersSurveyState`.

- [ ] **Step 7: Commit**

```bash
cd bottle-tui
git add internal/controlplane/client.go internal/controlplane/eventstream.go internal/model/events.go internal/tui/dashboard.go internal/tui/dashboard_test.go
git commit -m "Surface GPS fix state on the bottle-tui dashboard"
```

---

## Manual Verification (on real Pi hardware)

Automated tests cover all business logic against fakes; `internal/gpio/chip.go`'s real hardware binding (Task 2) has no automated test. Once the button and LED are physically wired to the Pi's GPIO header at the pins configured via `BOTTLE_AGENT_BUTTON_PIN`/`BOTTLE_AGENT_LED_PIN`:

1. Deploy the updated `bottle-agent` and restart the `bottle-agent` systemd service.
2. Confirm gpsd is running (`systemctl status gpsd`) and has a satellite view (outdoors or near a window); confirm the LED lights up once gpsd reports a fix, and turns off if you cover the GPS antenna and the fix is lost.
3. Hold the button for less than the configured `BOTTLE_AGENT_BUTTON_HOLD` and release — confirm the Pi does *not* shut down.
4. Hold the button past the configured duration — confirm `systemctl poweroff` runs (check `journalctl -u bottle-agent` for the "gpio: power button" / shutdown log, and confirm the Pi powers down).
5. From a paired `bottle-tui`, confirm the dashboard's `gps:` line reflects `locked`/`no fix` correctly, and that a `power_button_shutdown` event appears in the event log just before the connection drops.
