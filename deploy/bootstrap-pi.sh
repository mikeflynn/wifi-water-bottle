#!/usr/bin/env bash
#
# One-command Pi bootstrap, run from your laptop at the repo root:
#
#   ./deploy/bootstrap-pi.sh mike@bottle.local
#
# Cross-compiles bottle-agent for the Pi, installs it with its systemd unit,
# generates the CA and certificates, pairs a laptop profile, configures the
# direct Ethernet link (including DHCP so the laptop needs no manual IP),
# then copies the profile back and imports it into your OS credential store.
#
# Needs: an SSH-reachable Pi with passwordless sudo, and Go on this machine.
# Safe to re-run — it upgrades the binary and reuses the existing CA.

set -euo pipefail

PROFILE="laptop-profile"
IFACE="eth0"
SKIP_IMPORT=false
TARGET=""
REMOTE_DIR=".bottle-bootstrap"
PROFILE_DIR="/var/lib/bottle-agent/profiles"

usage() {
	cat <<'EOF'
usage: ./deploy/bootstrap-pi.sh <user>@<pi-host> [options]

  --profile NAME   laptop profile to issue and pair (default: laptop-profile)
  --iface IFACE    Pi interface for the direct link (default: eth0)
  --skip-import    copy the profile back but don't import it into the
                   credential store
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	--profile)
		PROFILE="${2:?--profile needs a value}"
		shift 2
		;;
	--iface)
		IFACE="${2:?--iface needs a value}"
		shift 2
		;;
	--skip-import)
		SKIP_IMPORT=true
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	-*)
		echo "unknown option: $1" >&2
		usage >&2
		exit 2
		;;
	*)
		if [ -n "$TARGET" ]; then
			echo "unexpected argument: $1" >&2
			exit 2
		fi
		TARGET="$1"
		shift
		;;
	esac
done

if [ -z "$TARGET" ]; then
	usage >&2
	exit 2
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

step() { printf '\n\033[1m== %s\033[0m\n' "$1"; }

step "Preflight"
command -v go >/dev/null || {
	echo "error: go not found on PATH" >&2
	exit 1
}
[ -d bottle-agent ] && [ -d bottle-tui ] || {
	echo "error: run this from the wifi-water-bottle repo" >&2
	exit 1
}
ssh -o BatchMode=yes -o ConnectTimeout=10 "$TARGET" true || {
	echo "error: cannot SSH to $TARGET non-interactively (key auth required)" >&2
	exit 1
}
echo "$TARGET reachable, go present"

WORK="$(mktemp -d)"
# The profile contains a private key; don't leave it in /tmp.
trap 'rm -rf "$WORK"' EXIT

step "Building bottle-agent for linux/arm64"
(cd bottle-agent && GOOS=linux GOARCH=arm64 go build -o "$WORK/bottle-agent" .)

step "Copying files to $TARGET:~/$REMOTE_DIR"
ssh "$TARGET" "mkdir -p ~/$REMOTE_DIR"
scp -q "$WORK/bottle-agent" \
	deploy/bottle-agent.service \
	deploy/bottle-agent-link.conf \
	deploy/pi-bootstrap-remote.sh \
	"$TARGET:$REMOTE_DIR/"

step "Running bootstrap on the Pi"
# SSH_CONNECTION is expanded by the login shell before sudo strips it, so the
# remote script can tell whether it is about to reconfigure the interface
# we're connected over.
ssh -t "$TARGET" \
	"chmod +x ~/$REMOTE_DIR/pi-bootstrap-remote.sh && sudo ~/$REMOTE_DIR/pi-bootstrap-remote.sh --profile '$PROFILE' --iface '$IFACE' --ssh-peer \"\${SSH_CONNECTION%% *}\""

step "Retrieving profile '$PROFILE'"
# tar over SSH rather than scp: the profile files are root-owned mode 0600.
ssh "$TARGET" "sudo tar -C '$PROFILE_DIR' -cf - '$PROFILE'" | tar -C "$WORK" -xf -
ls "$WORK/$PROFILE"

if [ "$SKIP_IMPORT" = true ]; then
	DEST="$HOME/$PROFILE"
	cp -R "$WORK/$PROFILE" "$DEST"
	echo "profile copied to $DEST; import it with:"
	echo "  cd bottle-tui && go run . control profile import \\"
	echo "    --ca $DEST/ca.pem --cert $DEST/client-cert.pem --key $DEST/client-key.pem --id $PROFILE"
else
	step "Importing profile into the credential store"
	(cd bottle-tui && go run . control profile import \
		--ca "$WORK/$PROFILE/ca.pem" \
		--cert "$WORK/$PROFILE/client-cert.pem" \
		--key "$WORK/$PROFILE/client-key.pem" \
		--id "$PROFILE")
fi

step "Done"
cat <<EOF
Plug the direct Ethernet cable into your laptop and leave the adapter on
DHCP — the Pi hands it 10.77.0.2. Then:

  cd bottle-tui && go run .

If the Pi bootstrap reported that it skipped the $IFACE step, run the
commands it printed from a local console first.
EOF
