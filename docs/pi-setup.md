# Pi setup (one time per Pi)

This is the one-time bootstrap that gets a fresh Raspberry Pi from "flashed SD card" to "plug in the Ethernet cable and run `bottle-tui`." It needs a working network connection to the Pi once (WiFi or your LAN, for SSH) and root/SSH access — after this, day-to-day use needs neither.

Sized for a single personal rig or a hobbyist building their own from the [Printables files](https://www.printables.com/model/1167677-wifi-water-bottle-skeleton) — not a fleet. No GPIO button, no separate pairing ceremony: everything happens locally over one SSH session.

## Flash and get SSH access

Flash Raspberry Pi OS **Bookworm, 64-bit**. In Raspberry Pi Imager, use "Edit Settings" before writing to set a hostname, enable SSH, and configure WiFi (or plug into your LAN with Ethernet) — this gets you SSH access on first boot without a monitor/keyboard. This network connection is temporary, just for this setup session.

**Connect over WiFi if you can.** The bootstrap reconfigures `eth0` into the dedicated laptop link, so an SSH session arriving over Ethernet is a session the setup would cut. Both the script and the manual path handle this, but WiFi avoids the detour.

```sh
ssh <user>@<pi-hostname-or-ip>.local
```

## The automated path

From your laptop, at the repo root:

```sh
./deploy/bootstrap-pi.sh <user>@<pi-hostname-or-ip>.local
```

That does everything below in one pass: cross-compiles the agent, installs it with its systemd unit, generates the CA and certificates, pairs a laptop profile, configures the direct Ethernet link and its DHCP server, starts the service, then copies the profile back and imports it into your Mac's credential store.

It needs Go on your laptop and passwordless sudo on the Pi. It's safe to re-run — the binary is upgraded in place and an existing CA and profile are reused, not replaced.

Useful flags:

- `--profile NAME` — pair a second laptop (`--profile work-laptop`). Reuses the existing CA; just adds a pairing.
- `--skip-import` — copy the profile to `~/NAME` but leave the credential-store import to you.
- `--iface IFACE` — use something other than `eth0` for the direct link.

If the script reports that it **skipped** the interface step, your SSH session was arriving over `eth0` and reconfiguring it would have disconnected you. Everything else is installed; run the commands it printed from a local console (or reconnect over WiFi and re-run).

Then skip to [Day to day](#day-to-day-after-this).

## The manual path

Exactly what the script does, if you'd rather do it by hand or need to debug a step.

### 1. Build and install the bottle-agent binary

From your laptop, in this repo:

```sh
cd bottle-agent
GOOS=linux GOARCH=arm64 go build -o bottle-agent .
scp bottle-agent <user>@<pi>:~/
scp ../deploy/bottle-agent.service <user>@<pi>:~/
scp ../deploy/bottle-agent-link.conf <user>@<pi>:~/   # used in step 4
```

On the Pi:

```sh
sudo mv bottle-agent /usr/local/bin/bottle-agent
sudo mv bottle-agent.service /etc/systemd/system/bottle-agent.service
sudo systemctl daemon-reload
sudo systemctl enable bottle-agent
```

`enable` is safe now — the unit has `ConditionPathExists=/etc/bottle-agent/pki/server-cert.pem`, so it won't actually start until after step 2.

### 2. Generate certs and pair your laptop

Still on the Pi:

```sh
sudo bottle-agent setup --profile laptop-profile
```

This generates a private CA and the Pi's own server certificate on first run (reused on later runs), issues a new client certificate for `laptop-profile`, and pairs its fingerprint directly into the local allowlist — no network round-trip, nothing to approve remotely. It prints the exact commands for the next step. The output files are under `./bottle-agent-profiles/laptop-profile/` and owned by root (`sudo scp` or `sudo chown` them off).

Running `setup` again with a different `--profile` name (e.g. for a second laptop) reuses the existing CA and just adds a new pairing — same one command, no extra ceremony.

```sh
scp -r bottle-agent-profiles/laptop-profile <you>@<laptop>:~/
```

### 3. Import the profile on your laptop

```sh
cd bottle-tui
go run . control profile import \
  --ca ~/laptop-profile/ca.pem \
  --cert ~/laptop-profile/client-cert.pem \
  --key ~/laptop-profile/client-key.pem \
  --id laptop-profile
```

### 4. The direct Ethernet link

The Pi's address is fixed: `10.77.0.1` is baked into the server certificate's SAN and pinned as the client's expected `ServerName`, so this part is not adjustable. The laptop's address is not pinned to anything, so the Pi hands it out over DHCP and your laptop needs no network configuration at all.

Run this from a local console, or over WiFi — **not** over an SSH session arriving on `eth0`, which the first command will drop.

```sh
sudo nmcli con add type ethernet ifname eth0 con-name bottle-agent-link \
  ipv4.method manual ipv4.addresses 10.77.0.1/30 ipv4.never-default yes \
  ipv6.method disabled connection.autoconnect yes
sudo nmcli con up bottle-agent-link
```

Bookworm ships a default `Wired connection 1` profile that will race this one on autoconnect; turn it off:

```sh
sudo nmcli con mod "Wired connection 1" connection.autoconnect no
```

Then the DHCP server. `deploy/bottle-agent-link.conf` is a DHCP-only dnsmasq config — no DNS listener, and it deliberately sends no router and no DNS-server option, because macOS ranks Ethernet above WiFi in service order and an offer carrying a gateway would steal your laptop's default route:

```sh
sudo apt-get install -y dnsmasq
sudo install -m 0644 bottle-agent-link.conf /etc/dnsmasq.d/bottle-agent-link.conf
sudo systemctl enable --now dnsmasq
sudo systemctl restart dnsmasq
```

On your Mac, leave the Ethernet adapter on "Using DHCP" — the default. Nothing to configure.

<details>
<summary>If you'd rather assign the laptop a static IP instead</summary>

Skip dnsmasq entirely and configure the Mac side yourself. In the GUI: System Settings → Network → (the Ethernet adapter connected to the Pi) → Details → TCP/IP → Configure IPv4: Manually, IP `10.77.0.2`, Subnet Mask `255.255.255.252`, no router. Or scriptably:

```sh
networksetup -listallnetworkservices                       # find the adapter's name
sudo networksetup -setmanual "USB 10/100/1000 LAN" 10.77.0.2 255.255.255.252
sudo networksetup -setdhcp   "USB 10/100/1000 LAN"         # to undo
```

Leaving the router argument off is what keeps this from taking over your default route. Consider making a separate Network Location for it so your normal networking is untouched when you switch back.

</details>

### 5. Start the service

```sh
sudo systemctl start bottle-agent
sudo systemctl status bottle-agent
```

## Day to day, after this

No SSH, no WiFi needed on the Pi. Plug the direct Ethernet cable into your laptop, run `bottle-tui`, and go. `bottle-agent` starts automatically on boot via systemd.

## GPIO power button and GPS-lock LED

Both are optional — if the hardware isn't wired up, `bottle-agent` logs that it's unavailable and starts normally without it.

**Power button** (default BCM pin **17**): wire a plain push button between that pin and a GND pin. `bottle-agent` uses the Pi's internal pull-up, so nothing external is needed — idle is high, pressed pulls the line to GND. Hold it down for **2 seconds** (default) to trigger `systemctl poweroff`; a short tap does nothing.

**GPS-lock LED** (default BCM pin **27**): wire an LED (with an appropriate current-limiting resistor) between that pin and GND. `bottle-agent` drives it high while `gpsd` reports a 3D fix and low otherwise.

Override the defaults with environment variables:

| Variable | Default | Meaning |
| --- | --- | --- |
| `BOTTLE_AGENT_BUTTON_PIN` | `17` | BCM pin the power button is wired to |
| `BOTTLE_AGENT_BUTTON_HOLD` | `2s` | how long the button must be held to trigger shutdown (Go duration syntax, e.g. `1500ms`) |
| `BOTTLE_AGENT_LED_PIN` | `27` | BCM pin the GPS-lock LED is wired to |

A malformed value for any one of these falls back to its default (logged, not fatal) — it will never take down the control plane or tunnel.

Since `bottle-agent` runs under systemd, set these by creating `/etc/bottle-agent/env` (one `KEY=value` per line) — the unit loads it via `EnvironmentFile=-/etc/bottle-agent/env` (the leading `-` makes a missing file harmless) — then `sudo systemctl restart bottle-agent`.

## Known gap

`bottle-tui control update` / the TUI's Update screen are wired to the protocol but the agent-side `Update` handler is a deliberate stub (`"update channel resolution is not implemented yet"`) — there's no release-publishing/signing pipeline yet for it to verify against. Provision and Survey are fully wired.

The same gap is why `bootstrap-pi.sh` builds and pushes the binary from your laptop rather than being a `curl … | bash` one-liner: there is no published release artifact for a script on the Pi to fetch.

## Verification

The Host implementation (`apt-get`/`systemctl`/GPS/radio detection, release staging) is unit-tested for *which* commands it builds, not real execution — there's no Pi, radio, or GPS in CI. The bootstrap scripts aren't unit-tested at all, for the same reason: they drive `apt-get`, `nmcli`, and `systemctl` against hardware CI doesn't have.

Run the existing hardware procedure once this is installed:

```sh
(cd bottle-agent && go test -race ./... && go vet ./...)
```

then `docs/pi-provision-update-test.md` on the real Pi.
