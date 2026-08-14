# Pi setup (one time per Pi)

This is the one-time bootstrap that gets a fresh Raspberry Pi from "flashed SD card" to "plug in the Ethernet cable and run `bottle-tui`." It needs a working network connection to the Pi once (WiFi or your LAN, for SSH) and root/SSH access — after this, day-to-day use needs neither.

Sized for a single personal rig or a hobbyist building their own from the [Printables files](https://www.printables.com/model/1167677-wifi-water-bottle-skeleton) — not a fleet. No GPIO button, no separate pairing ceremony: everything happens locally over one SSH session.

## 1. Flash and get SSH access

Flash Raspberry Pi OS **Bookworm, 64-bit**. In Raspberry Pi Imager, use "Edit Settings" before writing to set a hostname, enable SSH, and configure WiFi (or plug into your LAN with Ethernet) — this gets you SSH access on first boot without a monitor/keyboard. This network connection is temporary, just for this setup session.

```sh
ssh <user>@<pi-hostname-or-ip>.local
```

## 2. Build and install the bottle-agent binary

From your laptop, in this repo:

```sh
cd bottle-agent
GOOS=linux GOARCH=arm64 go build -o bottle-agent .
scp bottle-agent <user>@<pi>:~/
scp ../deploy/bottle-agent.service <user>@<pi>:~/
```

On the Pi:

```sh
sudo mv bottle-agent /usr/local/bin/bottle-agent
sudo mv bottle-agent.service /etc/systemd/system/bottle-agent.service
sudo systemctl daemon-reload
sudo systemctl enable bottle-agent
```

`enable` is safe now — the unit has `ConditionPathExists=/etc/bottle-agent/pki/server-cert.pem`, so it won't actually start until after step 3.

## 3. Generate certs and pair your laptop

Still on the Pi:

```sh
sudo bottle-agent setup --profile laptop-profile
```

This generates a private CA and the Pi's own server certificate on first run (reused on later runs), issues a new client certificate for `laptop-profile`, and pairs its fingerprint directly into the local allowlist — no network round-trip, nothing to approve remotely. It prints the exact commands for the next step. The output files are under `./bottle-agent-profiles/laptop-profile/` and owned by root (`sudo scp` or `sudo chown` them off).

Running `setup` again with a different `--profile` name (e.g. for a second laptop) reuses the existing CA and just adds a new pairing — same one command, no extra ceremony.

```sh
scp -r bottle-agent-profiles/laptop-profile <you>@<laptop>:~/
```

## 4. Import the profile on your laptop

```sh
cd bottle-tui
go run . control profile import \
  --ca ~/laptop-profile/ca.pem \
  --cert ~/laptop-profile/client-cert.pem \
  --key ~/laptop-profile/client-key.pem \
  --id laptop-profile
```

## 5. Static IP on the direct Ethernet link

This is deliberately a manual, documented step rather than something `setup` does for you automatically — it's the one change that could disconnect an active SSH session if scripted wrong.

On the Pi (Bookworm uses NetworkManager):

```sh
sudo nmcli con add type ethernet ifname eth0 con-name bottle-agent-link \
  ipv4.method manual ipv4.addresses 10.77.0.1/30 ipv4.never-default yes
sudo nmcli con up bottle-agent-link
```

On your Mac laptop: System Settings → Network → (the Ethernet adapter connected to the Pi) → Details → TCP/IP → Configure IPv4: Manually, IP `10.77.0.2`, Subnet Mask `255.255.255.252`, no router.

## 6. Start the service

```sh
sudo systemctl start bottle-agent
sudo systemctl status bottle-agent
```

## Day to day, after this

No SSH, no WiFi needed on the Pi. Plug the direct Ethernet cable into your laptop, run `bottle-tui`, and go. `bottle-agent` starts automatically on boot via systemd.

## Known gap

`bottle-tui control update` / the TUI's Update screen are wired to the protocol but the agent-side `Update` handler is a deliberate stub (`"update channel resolution is not implemented yet"`) — there's no release-publishing/signing pipeline yet for it to verify against. Provision and Survey are fully wired.

## Verification

The Host implementation (`apt-get`/`systemctl`/GPS/radio detection, release staging) is unit-tested for *which* commands it builds, not real execution — there's no Pi, radio, or GPS in CI. Run the existing hardware procedure once this is installed:

```sh
(cd bottle-agent && go test -race ./... && go vet ./...)
```

then `docs/pi-provision-update-test.md` on the real Pi.
