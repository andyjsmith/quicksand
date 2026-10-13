#!/bin/sh
# Installs the Quicksand agent on a Linux template (run as root).
#
#   ./install-linux.sh <server-public-key> [subnet]
#
# server-public-key is the output of "quicksand agent pubkey"; subnet is the
# agent network (default 10.99.0.0/24) and must match Quicksand's config.
set -eu
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
key=${1:?usage: $0 <server-public-key> [subnet]}
subnet=${2:-10.99.0.0/24}
dir=$(dirname "$0")

case $(uname -m) in
	x86_64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) echo "unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

install -m 0755 "$dir/quicksand-agent-linux-$arch" /usr/local/bin/quicksand-agent
install -d -m 0700 /etc/quicksand-agent
umask 077
cat > /etc/quicksand-agent/config.json <<JSON
{
  "authorizedKeys": ["$key"],
  "network": { "subnet": "$subnet" }
}
JSON
install -m 0644 "$dir/quicksand-agent.service" /etc/systemd/system/quicksand-agent.service
systemctl daemon-reload
systemctl enable --now quicksand-agent
echo "Installed. Logs: journalctl -u quicksand-agent"
