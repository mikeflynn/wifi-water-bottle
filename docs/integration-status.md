# Integrated client workflow status

The feature modules are now present in one worktree and build together:

- `bottle-agent/internal/lifecycle`: durable provisioning and signed/digest-verified update jobs.
- `bottle-agent/internal/tunnel`: fixed-destination Pi-side Kismet loopback relay.
- `bottle-tui/internal/tunnel`: laptop-loopback Kismet listener with TLS 1.3 mTLS requirements.
- `bottle-tui/internal/model`: bounded, redacted, reconnect-aware live-event state.
- `bottle-tui/internal/wigle`: WiGLE CSV v1.6 export and confirmation-gated upload.
- `bottle-tui/internal/provisioncontrol`: confirmation and request-ID guard for typed remote control calls.

## Operator sequence (authorized equipment only)

1. Connect the dedicated Ethernet link; configure Pi `eth0` as `10.77.0.1/30` and laptop Ethernet as `10.77.0.2/30`, with no default route on either side.
2. Open the Pi's physical pairing window and pair a laptop profile after verifying the Pi fingerprint out of band.
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

## Current integration gate

The repository does not contain the typed TLS/mTLS control-plane listener, pairing/profile store, generated RPC client, or event-stream transport that connect the listed components. In particular, `provisioncontrol.Client` is only an interface, `model.Stream` is transport-independent, and `MTLSStreamOpener` can only open a raw TLS stream; no protocol selects provisioning, update, survey, event, or Kismet operations after the handshake.

Consequently, the sequence above is the required contract, not an executable end-to-end laptop command path yet. Do not claim an Ethernet hardware test has passed until that transport is implemented and a Pi is physically connected. On this Mac, verification found no `10.77.0.0/30` route or Pi management endpoint.

## Automated verification available now

```sh
(cd bottle-agent && go test -race ./... && go vet ./...)
(cd bottle-tui && go test -race ./... && go vet ./... && go build ./...)
git diff --check
```

These tests cover provision idempotency/checkpoint recovery, update digest rejection and rollback, fixed-destination tunnel behavior and local reconnect, live-event buffering/resync/redaction, and WiGLE export plus mocked upload. The physical test procedure remains in `docs/pi-provision-update-test.md` and `docs/kismet-tunnel.md`.
