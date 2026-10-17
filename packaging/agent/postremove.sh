#!/bin/sh
# Runs after the quicksand-agent package is removed. Purging (deb) also
# deletes the agent's configuration and debconf answers.
set -e
if [ -d /run/systemd/system ]; then
	systemctl daemon-reload || true
fi
if [ "$1" = purge ]; then
	rm -rf /etc/quicksand-agent
	if [ -f /usr/share/debconf/confmodule ]; then
		. /usr/share/debconf/confmodule
		db_purge
	fi
fi
