#!/bin/sh
# preremove of the arkeep-agent .deb and .rpm packages.
#
# Arguments, by package manager:
#   deb: remove, or upgrade/deconfigure/failed-upgrade on the way to a new version
#   rpm: 0 when the package is removed, 1 or more on an upgrade
#
# Only a removal stops and disables the service: an upgrade leaves it running,
# and postinstall restarts it on the new binary.
set -e

case "$1" in
  remove | 0) ;;
  *) exit 0 ;;
esac

command -v systemctl >/dev/null 2>&1 || exit 0
if [ -d /run/systemd/system ]; then
  systemctl stop arkeep-agent.service || true
fi
systemctl disable arkeep-agent.service >/dev/null 2>&1 || true
