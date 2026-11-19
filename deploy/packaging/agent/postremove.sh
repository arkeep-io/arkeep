#!/bin/sh
# postremove of the arkeep-agent .deb and .rpm packages.
#
# Arguments, by package manager:
#   deb: remove, purge, or upgrade/failed-upgrade/abort-* around a new version
#   rpm: 0 when the package is removed, 1 or more on an upgrade
#
# A removal keeps /var/lib/arkeep-agent, the agent's certificate and identity:
# installing the package again brings the same agent back. Only a purge
# (deb only: dpkg also deletes /etc/arkeep/agent.env then) deletes it, and the
# agent then enrolls as a new one. Its snapshots stay in their destinations.
set -e

case "$1" in
  purge) rm -rf /var/lib/arkeep-agent ;;
  remove | 0) ;;
  *) exit 0 ;;
esac

if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi
