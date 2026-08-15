# Wifi Water Bottle

A Raspberry Pi mounted in a water bottle for war driving or other wireless network experiments. The Pi is powered by a battery pack and controlled remotely via `bottle-tui`, a terminal app that runs on your laptop.

## Links

- [Project Page on hydrox.fun](https://hydrox.fun/projects/dew-the-wifi/)
- [Printables](https://www.printables.com/model/1167677-wifi-water-bottle-skeleton)
- [Pi setup (one time per Pi)](docs/pi-setup.md)
- [Integrated client workflow status](docs/integration-status.md)
- [Pi provision/update hardware test](docs/pi-provision-update-test.md)
- [Secure Kismet tunnel contract](docs/kismet-tunnel.md)

## Getting started

**1. Set up the Pi (once per Pi).** Flash it, get SSH access, then from this repo:

```sh
./deploy/bootstrap-pi.sh <user>@<pi-hostname>.local
```

That installs `bottle-agent`, pairs a laptop profile (all local, no network round-trip), configures the direct Ethernet link, and imports the profile into your credential store. Full walkthrough, including doing it by hand: [docs/pi-setup.md](docs/pi-setup.md).

**2. Import the profile on your laptop** — the bootstrap script already did this. By hand:

```sh
cd bottle-tui
go run . control profile import --ca ca.pem --cert client-cert.pem --key client-key.pem --id laptop-profile
```

(the exact command, with real file paths, is printed at the end of Pi setup)

**3. Day to day: plug in the direct Ethernet cable and launch the TUI.**

```sh
cd bottle-tui
go run .
```

That's the interactive console — dashboard, provision, update, survey with live logs, Kismet tunnel, and WiGLE screens. See [docs/integration-status.md](docs/integration-status.md) for keybindings.

## Pi control-plane workflow

The laptop operator path uses a typed JSON-framed RPC over TLS 1.3 mTLS. The Pi endpoint is fixed at `10.77.0.1:7443`; the client certificate, private key, and pinned CA are loaded from the OS secure store.

Everything above is also available as scripted, non-interactive commands — useful for automation or the hardware test procedure:

```sh
cd bottle-tui
go run . control provision --request-id provision-2026-08-14 --confirm
go run . control survey start --confirm
go run . control logs
go run . control tunnel --port 2501
```

The tunnel is deliberately limited to laptop `127.0.0.1` and the Pi's Kismet literal loopback — there is no arbitrary remote-shell operation. See [docs/integration-status.md](docs/integration-status.md) and [docs/pi-provision-update-test.md](docs/pi-provision-update-test.md) for the full direct-Ethernet procedure.

## WiGLE export and upload

`bottle-tui` accepts a portable JSON array of Wi-Fi observations (`bssid`, `ssid`, `auth_mode`, `first_seen` in RFC3339, `channel`, `frequency_mhz`, `rssi`, `latitude`, `longitude`, `altitude_meters`, and `accuracy_meters`). It validates records, lowercases BSSIDs, preserves supplied UTC timestamps and location metadata, and reports skipped malformed records before any action.

```sh
cd bottle-tui
go run . wigle preview --input capture.json
go run . wigle export --input capture.json --output capture.wiglecsv
```

The output is WiGLE CSV v1.6 with Wi-Fi rows only. Cell and Bluetooth rows, RCOIs, and manufacturer IDs are unsupported because the capture interchange does not contain reliable values for them.

Uploading is external disclosure. Create a WiGLE API name/token at https://wigle.net/account, authorize the upload under your WiGLE account, then save it in the operating system credential store (macOS Keychain; Linux Secret Service):

```sh
# Reads two whitespace-delimited values from standard input; neither is written to config or logs.
go run . wigle credentials set
go run . wigle upload --input capture.json --confirm
```

Upload cannot occur without `--confirm`. The client sends multipart form data to WiGLE's `/api/v2/file/upload` endpoint using HTTP Basic authentication, retries only transport failures and HTTP 429/5xx (three attempts with a one-second delay), and reports the WiGLE transaction ID and attempt count. It does not retry other HTTP errors, so accidental duplicate submissions are avoided as far as WiGLE's non-idempotent upload endpoint permits. WiGLE currently documents a 180 MiB file limit and a maximum of 200 archive members; this client uploads a single CSV and does not archive files.
