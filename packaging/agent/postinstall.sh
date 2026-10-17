#!/bin/sh
# Runs after the quicksand-agent package is installed or upgraded (deb:
# configure; rpm: 1 or 2). The .deb configures the agent with the key from
# its debconf prompt; otherwise "quicksand-agent install -key ..." does.
set -e
case "$1" in configure | 1 | 2) ;; *) exit 0 ;; esac
config=/etc/quicksand-agent/config.json

key= subnet=
if [ "$1" = configure ] && [ -f /usr/share/debconf/confmodule ]; then
	. /usr/share/debconf/confmodule
	db_get quicksand-agent/key && key=$RET
	db_get quicksand-agent/subnet && subnet=$RET
	db_stop
fi

[ -d /run/systemd/system ] && systemctl daemon-reload

if [ -n "$key" ] && { [ ! -f "$config" ] || [ "${DEBCONF_RECONFIGURE:-}" = 1 ]; }; then
	quicksand-agent install -key "$key" ${subnet:+-subnet "$subnet"} ||
		echo "Could not configure the agent; run: quicksand-agent install -key ed25519:... [-subnet CIDR]" >&2
elif [ -f "$config" ]; then
	if [ -d /run/systemd/system ]; then
		systemctl enable quicksand-agent >/dev/null 2>&1
		systemctl restart quicksand-agent
	fi
else
	echo "To start the agent, run: quicksand-agent install -key ed25519:... [-subnet CIDR]"
fi
