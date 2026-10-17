#!/bin/sh
# Stops the service when the quicksand package is removed, not upgraded
# (deb: remove; rpm: 0).
set -e
case "$1" in remove | 0) ;; *) exit 0 ;; esac
if [ -d /run/systemd/system ]; then
	systemctl disable --now quicksand >/dev/null 2>&1 || true
elif command -v systemctl >/dev/null; then
	systemctl disable quicksand >/dev/null 2>&1 || true
fi
