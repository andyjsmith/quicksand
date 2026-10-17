#!/bin/sh
# Runs after the quicksand package is removed. Purging (deb) also deletes
# /etc/quicksand (configuration and certificate); the database in
# /var/lib/quicksand and the quicksand user are kept.
set -e
if [ -d /run/systemd/system ]; then
	systemctl daemon-reload || true
fi
if [ "$1" = purge ]; then
	rm -rf /etc/quicksand
fi
