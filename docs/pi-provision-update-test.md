# Pi provisioning and update operator test procedure

This procedure exercises the Pi-side lifecycle workflow after the mTLS control-plane and TUI transport are deployed. Run it only on equipment and RF environments you are authorized to administer.

## Preconditions

- Raspberry Pi 4 or 5, 64-bit Raspberry Pi OS Bookworm, arm64.
- `eth0` is configured as `10.77.0.1/30`; laptop is `10.77.0.2/30`. Neither side takes a default route from this link.
- At least 2 GiB free storage; one supported radio and visible GPS hardware.
- `bottle-agent` runs as root, owns `/var/lib/bottle-agent` and `/var/log/bottle-agent`, and its gRPC listener is limited to `10.77.0.1:7443` with TLS 1.3 mTLS.
- Signed update manifests are verified with the release trust root before calling the lifecycle updater. Do not bypass verification for a local test artifact.

## Fresh Pi

1. Flash Bookworm 64-bit, configure the static `eth0` address, and boot with Ethernet connected.
2. Pair from the laptop during an explicitly opened, physical pairing window. Verify the displayed Pi fingerprint out of band.
3. In the TUI, choose `Provision`. Inspect the plan, including package list and proposed config paths, then explicitly confirm.
4. Observe durable phases: `preflight`, `backup`, `packages`, `configure`, `services`, `health`, `complete`.
5. Verify `systemctl is-active bottle-agent kismet`; verify Kismet listens only on loopback (`ss -ltnp`); verify `bottle-agent` only listens at `10.77.0.1:7443`; verify owner-only modes for config, credentials, and state.
6. Re-run the same request ID after a deliberate laptop reconnect. It must return the original successful job and execute no installation steps a second time.

## Already-configured Pi and retry safety

1. Put a recognizable, user-managed value in `/etc/kismet/kismet.conf`.
2. Select `Provision` without confirmation. The job must enter `needs_input`, list `/etc/kismet/kismet.conf`, and perform no command.
3. Confirm. Verify a timestamped backup was created before any configuration work and the user configuration was not silently replaced.
4. Interrupt a provision at a checkpoint (for example, disconnect Ethernet after `packages`). Reconnect and retry with the same request ID. Verify it resumes from the first incomplete checkpoint and retains prior completed checkpoints.
5. Use a signed manifest with an intentionally incorrect SHA-256. Verify staging and activation do not occur.
6. Use a valid signed release whose post-activation health check is forced to fail. Verify the release switches back atomically, `/var/lib/bottle-agent`, exports, logs, credentials, and configuration remain present, and the job reports `UPDATE_ROLLED_BACK`.

## Automated coverage

From the repository root:

```sh
(cd bottle-agent && go test -race ./... && go vet ./...)
(cd bottle-tui && go test ./... && go vet ./...)
```

The lifecycle tests cover confirmation gating, arm64 preflight, idempotent job retries, persistent job checkpoints, digest rejection before staging, preservation backup, and health-check rollback. The TUI control tests cover explicit confirmation and typed request propagation. Hardware/network validation remains a physical smoke test because Kismet, radios, GPS, systemd, and network namespace semantics are unavailable in unit tests.
