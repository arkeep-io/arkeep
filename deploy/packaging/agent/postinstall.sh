#!/bin/sh
# postinstall of the arkeep-agent .deb and .rpm packages.
#
# Arguments, by package manager:
#   deb: configure <previously configured version, empty on a first install>
#        (abort-upgrade, abort-remove... on error paths: nothing to do)
#   rpm: 1 on a first install, 2 or more on an upgrade
#
# A first install enables the service, and starts it only once agent.env has
# a server address: without one the agent would exit and restart in a loop.
# An upgrade restarts the service if it is running, and leaves its enabled or
# disabled state as the administrator set it.
set -e

env_file=/etc/arkeep/agent.env
unit=arkeep-agent.service

case "$1" in
  configure) if [ -n "${2:-}" ]; then action=upgrade; else action=install; fi ;;
  1) action=install ;;
  [0-9]*) action=upgrade ;;
  *) exit 0 ;;
esac

# The file carries the agent secret. A package manager installs a config file
# with the mode of the package, but one kept from a manual install may be wider.
if [ -f "$env_file" ]; then
  chmod 0600 "$env_file"
fi

if [ -f /etc/systemd/system/$unit ]; then
  echo "arkeep-agent: /etc/systemd/system/$unit, left by a manual install, overrides the packaged unit." >&2
  echo "arkeep-agent: it now runs /usr/bin/arkeep-agent; remove it and /usr/local/bin/arkeep-agent, then run 'systemctl daemon-reload'." >&2
fi

# No systemctl (a container, a chroot): the files are in place, nothing to run.
command -v systemctl >/dev/null 2>&1 || exit 0
# systemd not running (a container, an image build): enabling still works offline.
running=false
if [ -d /run/systemd/system ]; then
  running=true
  systemctl daemon-reload || true
fi

if [ "$action" = upgrade ]; then
  if [ "$running" = true ]; then
    systemctl try-restart $unit || true
  fi
  exit 0
fi

systemctl enable $unit >/dev/null 2>&1 || true
if grep -Eq '^ARKEEP_SERVER_ADDR=[^[:space:]]+' "$env_file" 2>/dev/null; then
  if [ "$running" = true ]; then
    systemctl start $unit || true
  fi
else
  echo "arkeep-agent: set ARKEEP_SERVER_ADDR and ARKEEP_AGENT_SECRET in $env_file, then run 'systemctl start arkeep-agent'."
fi
