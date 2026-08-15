# Secure Kismet tunnel

The tunnel is intentionally not an SSH forward and does not create a TCP listener on the Pi.

## Security boundary

- Kismet must listen at a literal loopback address on the Pi, for example `127.0.0.1:2501`. `bottle-agent/internal/tunnel.NewServer` rejects hostnames, wildcard addresses, LAN addresses, and IPv6 unspecified addresses.
- The laptop tunnel listens exclusively on `127.0.0.1:<port>` (default policy: 2501; `0` selects an explicit ephemeral port). A requested occupied port returns `LOCAL_PORT_UNAVAILABLE`; there is no silent fallback.
- Each browser connection opens a new TLS 1.3 stream to the Pi's fixed control-plane address. `MTLSStreamOpener` requires both the profile-pinned private CA and a complete client certificate/private key. It rejects system-root and anonymous-TLS fallback.
- The Pi agent must invoke `Server.Serve` only after its TLS listener has completed a TLS 1.3 mTLS handshake and checked the peer certificate fingerprint against its paired-device allowlist. This relay never accepts a destination host or port from the client.
- Client key material is injected into `tls.Config` from the OS keychain by the profile layer. This package persists no certificate, private key, token, or CA data and never logs them.

## Lifecycle / UX contract

Call `tunnel.Start(ctx, localPort, opener)`. Display `Tunnel.URL()` to the operator and consume `Tunnel.Events()` for status:

- `starting`: the loopback listener is available.
- `connected`: an authenticated control-plane stream is serving a browser request.
- `reconnecting`: the stream could not be opened or was lost. The loopback listener stays alive, so Kismet/browser reconnects create a fresh authenticated stream.
- `closed`: shutdown is complete.

Call `Tunnel.Close()` for clean shutdown. Connection-loss errors are exposed through `Event.Err`/`LastError`; callers should map them to the stable UI reason `PI_UNREACHABLE` without printing TLS credentials or raw certificate content.

## Reproducible verification

Run from the repository root:

```sh
go test -race ./bottle-agent/internal/tunnel ./bottle-tui/internal/tunnel
```

The tests verify: fixed destination rejection for non-loopback Pi addresses; byte-for-byte HTTP/WebSocket-safe relay to a loopback Kismet fixture; laptop listener loopback binding; configured-port collision handling; and recovery by accepting another browser request after an mTLS stream-open failure.

For a hardware smoke test, bind Kismet only to `127.0.0.1:2501` on the Pi, start the authenticated agent tunnel service, then run the TUI tunnel command with a paired profile. `curl -f http://127.0.0.1:2501/` on the laptop must succeed. Attempts to access `http://10.77.0.1:2501/` or the Pi Wi-Fi address on port 2501 must fail; the only TCP port permitted on Pi `eth0` is 7443, and only after mTLS authorization. (Pi `eth0` also runs dnsmasq's DHCP server on UDP/67 to address the laptop end of the link — see `docs/pi-setup.md`. It is configured with `port=0`, so it serves no DNS.)
