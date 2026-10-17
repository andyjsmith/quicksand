#!/bin/sh
# Runs after the quicksand package is installed or upgraded (deb: configure;
# rpm: 1 or 2). Sets up what deploy/server/install.sh does for a by-hand
# install. Existing configuration, certificates and data are never touched.
set -e
case "$1" in configure | 1 | 2) ;; *) exit 0 ;; esac

id quicksand >/dev/null 2>&1 ||
	useradd --system --home-dir /var/lib/quicksand --shell /usr/sbin/nologin quicksand
install -d -m 0750 -o root -g quicksand /etc/quicksand
install -d -m 0700 -o quicksand -g quicksand /var/lib/quicksand

# Self-signed certificate for the host's name and addresses, until replaced
# with one from a real CA (same paths).
if [ ! -f /etc/quicksand/tls.crt ]; then
	names="DNS:$(hostname),DNS:$(hostname -f 2>/dev/null || hostname),DNS:localhost,IP:127.0.0.1"
	for ip in $(hostname -I 2>/dev/null); do names="$names,IP:$ip"; done
	openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 825 \
		-subj "/CN=$(hostname)" -addext "subjectAltName=$names" \
		-keyout /etc/quicksand/tls.key -out /etc/quicksand/tls.crt 2>/dev/null
	chgrp quicksand /etc/quicksand/tls.key
	chmod 0640 /etc/quicksand/tls.key
	echo "Created a self-signed certificate for: $names"
fi

systemd=
[ -d /run/systemd/system ] && systemd=1
[ -z "$systemd" ] || systemctl daemon-reload

if [ -f /etc/quicksand/config.json ]; then
	chgrp quicksand /etc/quicksand/config.json
	chmod 0640 /etc/quicksand/config.json
	if [ -n "$systemd" ]; then
		systemctl enable quicksand >/dev/null 2>&1
		systemctl restart quicksand
		echo "Quicksand (re)started. Logs: journalctl -u quicksand"
	fi
else
	cat <<'EOF'
Quicksand is installed. To finish setting it up:
  1. Write /etc/quicksand/config.json, starting from
     /usr/share/doc/quicksand/config.example.json
  2. systemctl enable --now quicksand
  3. sudo -u quicksand quicksand user add -admin <username>
EOF
fi
