# Integrated client workflow status

The feature modules are now present in one worktree and build together:

- `bottle-agent/internal/lifecycle`: durable provisioning and signed/digest-verified update jobs.
- `bottle-agent/internal/host`: the real, OS-backed `lifecycle.Host` — shells to `apt-get`/`systemctl`, reads `/etc/os-release`/GPS/radio hardware, manages release directories.
- `bottle-agent/internal/eventbus`: bounded, sequence-numbered, multi-subscriber event store backing `Handler.Events`.
- `bottle-agent/internal/agent`: the concrete `controlplane.Handler`, wiring lifecycle/survey/eventbus together.
- `bottle-agent/internal/pki`: local CA and certificate generation used by `bottle-agent setup` — no network round-trip, no external tooling.
- `bottle-agent` (main.go): the runnable service binary plus the `setup` subcommand; see `docs/pi-setup.md`.
- `bottle-agent/internal/tunnel`: fixed-destination Pi-side Kismet loopback relay.
- `bottle-tui/internal/tunnel`: laptop-loopback Kismet listener with TLS 1.3 mTLS requirements.
- `bottle-tui/internal/model`: bounded, redacted, reconnect-aware live-event state.
- `bottle-tui/internal/wigle`: WiGLE CSV v1.6 export and confirmation-gated upload.
- `bottle-tui/internal/provisioncontrol`: confirmation and request-ID guard for typed remote control calls.
- `bottle-agent/internal/controlplane` and `bottle-tui/internal/controlplane`: TLS 1.3 mTLS JSON-framed typed RPC, pairing allowlist, secure keyring profile loading, cursor event subscription, and lifecycle/survey/status dispatch.
- `bottle-tui/internal/tui`: the interactive Bubble Tea operator console (see below), built on the modules above without changing their behavior.

## Interactive TUI

Running `bottle-tui` with no arguments launches an interactive console instead of the one-shot batch commands below. It is the default way to operate the Pi from the laptop; the scripted `control`/`wigle` subcommands remain unchanged for automation and the hardware test procedure.

```sh
cd bottle-tui
go run .
```

Seven screens, switched with `1`-`7`, `]`/`[` (bracket keys always work, even while a text field has focus), or `tab`/`shift+tab` within a form: dashboard, pairing (profile import), provision, update, survey (embeds the live, redacted, resync-aware event viewport, with `p` to pause/scroll-lock), tunnel (Kismet loopback, `t` to start, `c` to close), and WiGLE (preview/export/upload, `c` for credential entry). Provision, update, survey start/stop, and WiGLE upload all route through a single confirm modal before dispatching — provision/update require retyping the request ID or version, matching the confirmation the Pi itself enforces server-side; survey and upload use a yes/no prompt. `?` toggles the key-hint footer, `q`/`ctrl+c` quits (prompting first if a Kismet tunnel is still open).

## Operator sequence (authorized equipment only)

1. One-time per Pi: follow `docs/pi-setup.md` — install `bottle-agent`, run `bottle-agent setup --profile <name>` (generates the CA/certs and pairs the profile locally, no network round-trip), configure the static `eth0` address, and import the profile into `bottle-tui`.
2. Connect the dedicated Ethernet link; Pi `eth0` is `10.77.0.1/30`, laptop Ethernet is `10.77.0.2/30`, with no default route on either side.
3. Check the typed control-plane readiness, then provision with an explicit confirmation. The Pi must report `preflight`, `backup`, `packages`, `configure`, `services`, `health`, then `complete`.
4. Start/stop a survey and consume the cursor-based event stream for live logs and status. Treat a `resync_required` event as a visible history gap, not as a silent reconnect.
5. Start the Kismet tunnel, open its local `http://127.0.0.1:<port>` URL, and close it on exit. Never use a LAN listener or an arbitrary tunnel destination.
6. Preview captures, export WiGLE CSV, and upload only with an explicit confirmation:

   ```sh
   cd bottle-tui
   go run . wigle preview --input capture.json
   go run . wigle export --input capture.json --output capture.wiglecsv
   go run . wigle upload --input capture.json --confirm
   ```

7. Request a signed update with a stable request ID. On any failed health check, confirm the agent reports `UPDATE_ROLLED_BACK` and preserves state, logs, exports, and configuration.

## Laptop operator commands

Pairing happens once, locally on the Pi, via `bottle-agent setup --profile <name>` (see `docs/pi-setup.md`) — it generates the client certificate and calls `OpenPhysicalWindow`/`Pair`/`ClosePhysicalWindow` in-process against the same persistent allowlist file the running agent reads, with no network round-trip. The persistent allowlist is owner-only JSON state; no private key is stored on the Pi. Laptop CA/client material is imported once and then loaded only from the operating-system keychain.

```sh
cd bottle-tui
go run . control profile import --ca pi-ca.pem --cert laptop-cert.pem --key laptop-key.pem --id laptop-profile
go run . control provision --request-id provision-2026-08-14 --confirm
go run . control update --request-id update-2026-08-14 --version v2 --channel stable --confirm
go run . control survey start --confirm
go run . control logs
go run . control survey stop --confirm
go run . control tunnel --port 2501
```

Provisioning and updates require stable request IDs and explicit confirmation. The tunnel binds only to `127.0.0.1`; the Pi endpoint is fixed at `10.77.0.1:7443`.

## Current integration gate

The transport is a typed JSON-framed protocol over TLS 1.3 mTLS, and `bottle-agent` is now a real, installable service (`docs/pi-setup.md`): a runnable binary, a systemd unit, a real OS-backed `lifecycle.Host`, an event bus, and local CA/certificate bootstrap. Provision and Survey are fully wired end to end; Update is a deliberate stub (`internal/agent.Handler.Update` returns "not implemented yet") pending a release-publishing/signing story that doesn't exist yet.

What is **not** verified from a dev machine, and can't be: real `apt-get`/`systemctl` behavior, real GPS/radio detection, a real mTLS handshake against a real Pi, and the pinned `10.77.0.1:7443` listener actually binding (there is no such interface here). `internal/host` is tested for *which* commands it constructs, not their real effect. Do not claim a hardware acceptance result until the `docs/pi-provision-update-test.md` procedure has been run against a physically connected, freshly set-up Pi.

## Automated verification available now

```sh
(cd bottle-agent && go test -race ./... && go vet ./...)
(cd bottle-tui && go test -race ./... && go vet ./... && go build ./...)
git diff --check
```

These tests cover provision idempotency/checkpoint recovery, update digest rejection and rollback, fixed-destination tunnel behavior and local reconnect, live-event buffering/resync/redaction, and WiGLE export plus mocked upload. The physical test procedure remains in `docs/pi-provision-update-test.md` and `docs/kismet-tunnel.md`.
