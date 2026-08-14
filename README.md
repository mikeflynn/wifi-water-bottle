# Wifi Water Bottle

A Raspberry Pi mounted in a water bottle for war driving or other wireless network experiments. The Pi is powered by a battery pack and can be controlled remotely via the included bottle-tui application.

## Links

- [Project Page on hydrox.fun](https://hydrox.fun/projects/dew-the-wifi/)
- [Printables](https://www.printables.com/model/1167677-wifi-water-bottle-skeleton)

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
