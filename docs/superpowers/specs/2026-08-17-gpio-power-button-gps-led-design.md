# GPIO Power Button + GPS-Lock LED — Design

## Context

The water bottle's Pi is powered by a USB battery pack. The user has a plain
push button wired directly to GPIO pins (no power-cutting hardware/HAT) and
wants the agent to:

1. Detect a held button press and trigger a clean shutdown.
2. Light an LED when the GPS module has a satellite fix.

Note: because there's no physical power-latching circuit, this only covers
shutdown. Powering the Pi back on still requires physically reapplying power
(e.g. unplugging/replugging the battery pack) — `bottle-agent.service`
already starts automatically on boot, so no "power on" logic is needed in
the agent itself.

`bottle-agent` (Go) already follows a "thin hardware shim + testable core"
pattern: `internal/host.Host` wraps every OS/hardware touch point (shell
commands, filesystem, `os-release`, gpsd reachability) behind an interface so
business logic can be unit tested without root or real hardware
(`bottle-agent/internal/host/host.go`). This design follows the same
pattern for GPIO and gpsd fix status.

`Host.GPSVisible()` today only checks that gpsd is reachable over TCP — it
does not know whether gpsd has an actual satellite fix. Driving the LED
correctly requires real fix status, not just reachability.

## Architecture

Two new packages:

### `internal/gpsd`

A small client that opens one persistent TCP connection to gpsd, sends the
`WATCH` command (`{"class":"WATCH","enable":true,"json":true}`), and reads
streaming JSON reports. It looks for `"class":"TPV"` reports and treats
`mode >= 2` as a fix (2D or 3D). It exposes fix-state *changes* via a
callback the caller supplies, e.g.:

```go
type FixWatcher struct { /* ... */ }

func NewFixWatcher(addr string, onChange func(fix bool)) *FixWatcher
func (w *FixWatcher) Run(ctx context.Context) error
```

On connection loss or parse failure, `Run` reconnects with exponential
backoff (starting ~1s, capped ~30s) rather than returning an error, since
this is a long-running background watcher. While disconnected, it calls
`onChange(false)` (fix status unknown => treated as no fix, so the LED
doesn't show a stale "locked" state).

This is a separate concern from `Host.GPSVisible()`, which remains a
reachability check for provisioning's `systemctl` health flow. `GPSVisible()`
is not changed by this design.

### `internal/gpio`

Wraps `github.com/warthog618/go-gpiocdev` behind two small interfaces so
timing/business logic is unit-testable without a real `/dev/gpiochip*`:

```go
type InputLine interface {
    // WatchEdges invokes fn(active) on every level change until ctx is done.
    WatchEdges(ctx context.Context, fn func(active bool)) error
}

type OutputLine interface {
    Set(active bool) error
}
```

Real implementations (`chipInputLine`, `chipOutputLine`) open the configured
chip/line via go-gpiocdev. If the chip or line can't be opened (e.g. running
on a dev machine without GPIO hardware), construction returns an error that
callers log and treat as "feature disabled" rather than fatal.

Two components sit on top of these interfaces:

```go
type Button struct {
    line InputLine
    hold time.Duration
    onHeld func()
}
func (b *Button) Run(ctx context.Context) error
```

`Button.Run` watches edges; when the line goes active it starts a timer for
`hold`; if the line goes inactive before the timer fires, the timer is
cancelled (debounces short/accidental presses); if the timer fires while
still active, `onHeld` is called once (further edges are ignored until the
line goes inactive again, to avoid re-firing while held).

```go
type Led struct { line OutputLine }
func (l *Led) Set(active bool) error
```

Trivial — `gpsd.FixWatcher`'s `onChange` callback calls `led.Set(fix)`
directly.

## Wiring

In `main.go`'s `startService`, alongside the existing `host.New()` /
`agent.New()` construction:

```go
button, err := gpio.NewButton(cfg.buttonPin, cfg.buttonHold)
led, err := gpio.NewLed(cfg.ledPin)
fixWatcher := gpsd.NewFixWatcher(gpsdAddr, func(fix bool) {
    _ = led.Set(fix) // logged, not fatal, on error
    handler.SetGPSFix(fix) // updates Status(); publishes bus event
})
```

Both `button.Run(ctx)` and `fixWatcher.Run(ctx)` are started as goroutines
using the same cancellable context that governs the rest of the service
lifecycle (the one derived from `signal.NotifyContext` in `runService`), so
they stop cleanly on shutdown. If `gpio.NewButton`/`gpio.NewLed` fail to open
their lines (no hardware present), `startService` logs a warning and
continues without them — GPIO absence must never prevent the control plane
or tunnel from starting.

The button's `onHeld` callback publishes a `power_button_shutdown` bus event
*before* calling `h.Run(ctx, "systemctl poweroff")`, so any connected
`bottle-tui` has a chance to see the event before the connection drops.

## Config

New env vars, read the same way as the existing `BOTTLE_AGENT_*` vars in
`main.go` (`envOr`):

| Var | Default | Meaning |
|---|---|---|
| `BOTTLE_AGENT_BUTTON_PIN` | `17` | BCM pin for the power button (placeholder until hardware is finalized) |
| `BOTTLE_AGENT_BUTTON_HOLD` | `2s` | Hold duration (Go `time.Duration` string) before shutdown triggers |
| `BOTTLE_AGENT_LED_PIN` | `27` | BCM pin for the GPS-lock LED (placeholder) |

gpsd address reuses the existing default (`127.0.0.1:2947`, same as
`host.New()`'s `gpsdAddr`).

## Control-plane changes

- `controlplane.Status` gains a `GPSFix bool` field alongside `Ready`,
  `Survey`, `Message`.
- `agent.Handler` gains fix-state (mirroring how it already tracks
  `surveyState`), updated via a new `SetGPSFix(bool)` method called from the
  wiring above, and returned from `Status()`.
- New bus event kinds: `gps_fix_acquired` / `gps_fix_lost` (published on
  each fix-state transition, not on every gpsd report) and
  `power_button_shutdown`.

## Error handling

- Missing/unopenable GPIO hardware (`/dev/gpiochip*` absent, line busy,
  etc.): logged, feature disabled, rest of the agent starts normally.
- gpsd unreachable or connection drops: `FixWatcher` reconnects with
  backoff; LED and `Status.GPSFix` reflect "no fix" while disconnected.
- Malformed/unexpected gpsd JSON lines are skipped (not fatal) — gpsd's
  protocol includes several report classes; only `TPV` is relevant here.

## Testing

- `internal/gpio`: unit tests drive fake `InputLine`/`OutputLine`
  implementations to verify hold-timing/debounce logic (short press does
  not fire, held-past-duration fires once, held-then-released-then-held
  again fires again) and `Led.Set` pass-through — no real hardware, same
  pattern as `host_test.go`'s fake `CommandRunner`.
- `internal/gpsd`: unit tests run a fake TCP listener that sends canned JSON
  lines (including malformed ones and a mid-stream disconnect) to verify
  `TPV`/mode parsing, fix-change callback firing only on transitions, and
  reconnect-with-backoff behavior.
- `internal/agent` / `internal/controlplane`: extend existing handler tests
  to assert `Status().GPSFix` reflects `SetGPSFix` calls, and that the new
  event kinds are published with expected payloads.

## Out of scope

- Powering the Pi back on via GPIO (requires power-latching hardware not
  present in this setup).
- Any UI changes in `bottle-tui` to *display* `GPSFix`/the new events — this
  design only adds them to the wire protocol; consuming them in the TUI is a
  separate follow-up if wanted.
