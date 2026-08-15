#!/usr/bin/env bash
#
# The on-Pi half of the bootstrap. Normally invoked for you by
# deploy/bootstrap-pi.sh from your laptop, but it is deliberately standalone:
# scp this plus bottle-agent, bottle-agent.service, and
# bottle-agent-link.conf into one directory on the Pi and run it by hand if
# you prefer.
#
#   sudo ./pi-bootstrap-remote.sh --profile laptop-profile
#
# Installs the agent, generates certs, pairs a laptop profile, and configures
# the direct Ethernet link. Safe to re-run: an existing CA and an existing
# profile are reused rather than replaced.

set -euo pipefail

PROFILE="laptop-profile"
IFACE="eth0"
CON_NAME="bottle-agent-link"
PROFILE_DIR="/var/lib/bottle-agent/profiles"
SSH_PEER="${SSH_CONNECTION%% *}"
SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
	cat <<'EOF'
usage: sudo pi-bootstrap-remote.sh [--profile NAME] [--iface IFACE] [--ssh-peer IP]

  --profile NAME   laptop profile to issue and pair (default: laptop-profile)
  --iface IFACE    interface for the direct link (default: eth0)
  --ssh-peer IP    address this session arrived from; used to avoid
                   reconfiguring the interface you are connected over.
                   Defaults to $SSH_CONNECTION, which sudo usually strips —
                   bootstrap-pi.sh passes it explicitly.
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
	--ssh-peer)
		SSH_PEER="${2-}"
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "unknown argument: $1" >&2
		usage >&2
		exit 2
		;;
	esac
done

if [ "$(id -u)" -ne 0 ]; then
	echo "error: must run as root (use sudo)" >&2
	exit 1
fi

for f in bottle-agent bottle-agent.service bottle-agent-link.conf; do
	if [ ! -f "$SRC_DIR/$f" ]; then
		echo "error: $f not found next to this script (looked in $SRC_DIR)" >&2
		exit 1
	fi
done

step() { printf '\n== %s\n' "$1"; }

step "Installing the agent binary and systemd unit"
if systemctl is-active --quiet bottle-agent; then
	echo "bottle-agent is running; stopping it to replace the binary"
	systemctl stop bottle-agent
fi
install -m 0755 -o root -g root "$SRC_DIR/bottle-agent" /usr/local/bin/bottle-agent
install -m 0644 -o root -g root "$SRC_DIR/bottle-agent.service" /etc/systemd/system/bottle-agent.service
systemctl daemon-reload
systemctl enable bottle-agent
# enable is safe before certs exist: the unit's ConditionPathExists on
# server-cert.pem keeps it from actually starting until setup has run.

step "Generating certificates and pairing profile '$PROFILE'"
if [ -d "$PROFILE_DIR/$PROFILE" ]; then
	echo "profile '$PROFILE' already exists at $PROFILE_DIR/$PROFILE — reusing it"
	echo "(pass --profile with a different name to pair another laptop)"
else
	mkdir -p "$PROFILE_DIR"
	chmod 0700 "$PROFILE_DIR"
	/usr/local/bin/bottle-agent setup --profile "$PROFILE" --out "$PROFILE_DIR"
fi

step "Configuring the direct link on $IFACE"
# Reconfiguring the interface this SSH session arrives on would drop the
# session mid-run and leave a half-configured Pi you need a monitor to
# recover. Detect that and hand the commands back instead.
ssh_iface=""
if [ -n "${SSH_PEER:-}" ]; then
	ssh_iface="$(ip -o route get "$SSH_PEER" 2>/dev/null | grep -o ' dev [^ ]*' | awk '{print $2}' || true)"
fi

if [ "$ssh_iface" = "$IFACE" ]; then
	cat >&2 <<EOF
SKIPPED: this session arrived over $IFACE (from $SSH_PEER), so reconfiguring
it now would disconnect you. Everything else is installed. Finish from a
local console, or reconnect over Wi-Fi and re-run this script. The remaining
commands are:

  sudo nmcli con add type ethernet ifname $IFACE con-name $CON_NAME \\
    ipv4.method manual ipv4.addresses 10.77.0.1/30 ipv4.never-default yes
  sudo nmcli con up $CON_NAME
  sudo apt-get install -y dnsmasq
  sudo install -m 0644 $SRC_DIR/bottle-agent-link.conf /etc/dnsmasq.d/$CON_NAME.conf
  sudo systemctl enable --now dnsmasq && sudo systemctl restart dnsmasq
  sudo systemctl start bottle-agent

EOF
	link_configured=false
else
	if nmcli -t -g NAME connection show | grep -qx "$CON_NAME"; then
		echo "connection '$CON_NAME' exists; updating it"
		nmcli con mod "$CON_NAME" ipv4.method manual ipv4.addresses 10.77.0.1/30 \
			ipv4.never-default yes ipv6.method disabled connection.autoconnect yes
	else
		nmcli con add type ethernet ifname "$IFACE" con-name "$CON_NAME" \
			ipv4.method manual ipv4.addresses 10.77.0.1/30 \
			ipv4.never-default yes ipv6.method disabled connection.autoconnect yes
	fi

	# Any other saved profile for this interface (Bookworm ships "Wired
	# connection 1") will race ours on autoconnect. This link is dedicated to
	# the bottle, so stand the others down.
	while IFS=: read -r name device type; do
		[ "$type" = "802-3-ethernet" ] || continue
		[ "$name" != "$CON_NAME" ] || continue
		[ "$device" = "$IFACE" ] || [ -z "$device" ] || continue
		echo "disabling autoconnect on competing profile '$name'"
		nmcli con mod "$name" connection.autoconnect no || true
	done < <(nmcli -t -f NAME,DEVICE,TYPE connection show)

	nmcli con up "$CON_NAME"

	step "Installing dnsmasq for laptop-side DHCP"
	if ! dpkg -s dnsmasq >/dev/null 2>&1; then
		DEBIAN_FRONTEND=noninteractive apt-get install -y dnsmasq
	fi
	install -m 0644 -o root -g root "$SRC_DIR/bottle-agent-link.conf" "/etc/dnsmasq.d/$CON_NAME.conf"
	systemctl enable dnsmasq
	systemctl restart dnsmasq
	link_configured=true
fi

if [ "$link_configured" = true ]; then
	step "Starting bottle-agent"
	systemctl start bottle-agent
	systemctl --no-pager --full status bottle-agent || true
else
	echo "not starting bottle-agent: it binds 10.77.0.1:7443, which does not exist yet"
fi

step "Done"
echo "profile material: $PROFILE_DIR/$PROFILE (root-owned, mode 0600)"
