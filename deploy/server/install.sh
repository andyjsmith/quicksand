#!/bin/sh
# Installs or upgrades Quicksand on a Debian-like host. Run as root from the
# directory built by scripts/build-server.sh:
#
#   sudo ./install.sh
#
# Existing configuration, keys, certificates and data are never overwritten.
set -eu
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
dir=$(cd "$(dirname "$0")" && pwd)

id quicksand >/dev/null 2>&1 ||
	useradd --system --home-dir /var/lib/quicksand --shell /usr/sbin/nologin quicksand
install -d -m 0750 -o root -g quicksand /etc/quicksand
install -d -m 0700 -o quicksand -g quicksand /var/lib/quicksand

install -m 0755 "$dir/quicksand" /usr/local/bin/quicksand
install -m 0644 "$dir/quicksand.service" /etc/systemd/system/quicksand.service

# Self-signed certificate for the host's name and addresses, until replaced
# with one from a real CA (same paths).
if [ ! -f /etc/quicksand/tls.crt ]; then
	names="DNS:$(hostname),DNS:$(hostname -f 2>/dev/null || hostname),DNS:localhost,IP:127.0.0.1"
	for ip in $(hostname -I); do names="$names,IP:$ip"; done
	openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 825 \
		-subj "/CN=$(hostname)" -addext "subjectAltName=$names" \
		-keyout /etc/quicksand/tls.key -out /etc/quicksand/tls.crt 2>/dev/null
	chgrp quicksand /etc/quicksand/tls.key
	chmod 0640 /etc/quicksand/tls.key
	echo "Created a self-signed certificate for: $names"
fi

if [ -f /etc/quicksand/config.json ]; then
	chgrp quicksand /etc/quicksand/config.json
	chmod 0640 /etc/quicksand/config.json
	systemctl daemon-reload
	systemctl enable quicksand >/dev/null
	systemctl restart quicksand
	echo "Quicksand (re)started. Logs: journalctl -u quicksand"
else
	systemctl daemon-reload
	echo "Write /etc/quicksand/config.json, then: systemctl enable --now quicksand"
fi
