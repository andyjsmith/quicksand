#!/bin/sh
# debconf config script for the quicksand-agent .deb: asks for the server's
# public key, unless the agent is already configured (dpkg-reconfigure asks
# again). Preseed quicksand-agent/key and quicksand-agent/subnet for
# unattended installs.
set -e
. /usr/share/debconf/confmodule

if [ -f /etc/quicksand-agent/config.json ] && [ "${DEBCONF_RECONFIGURE:-}" != 1 ]; then
	exit 0
fi
db_input high quicksand-agent/key || true
db_input medium quicksand-agent/subnet || true
db_go || true
