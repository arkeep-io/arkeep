#!/usr/bin/env bash
# test-agent-packages.sh — installs the agent .deb and .rpm built by
# GoReleaser in Debian, Ubuntu and Fedora containers, and checks what the
# package and its scripts (deploy/packaging/agent) do on install, upgrade,
# removal and purge.
#
# Usage: scripts/test-agent-packages.sh [<dist dir>]   (default: dist)
#
# Needs Docker, and the linux_amd64 packages in <dist dir>, for instance from
# `goreleaser release --snapshot --clean --skip=docker`. systemd does not run
# in the containers: the checks cover the files and the enabled state, which
# systemctl manages offline, not a running service.
set -euo pipefail

dist=$(cd "${1:-dist}" && pwd)
deb=arkeep-agent_linux_amd64.deb
rpm=arkeep-agent_linux_amd64.rpm
for f in "$deb" "$rpm"; do
  [ -f "${dist}/${f}" ] || { echo "missing ${dist}/${f}" >&2; exit 1; }
done

# Pinned by digest (ADR 0013).
debian=debian:13@sha256:9c6e381421d0f48d1b8133e573f33c185ee16cbf505eda9c4a8a4791b25ea8a6
ubuntu=ubuntu:26.04@sha256:d66b366dac61b575bee2aef07e9c26e8070096fbe84a3d4d3eb0da069b5d76db
fedora=fedora:44@sha256:15da97ed9aa94dd3d92c3925eb89520f6c80e07ea7ee24cba848d1f8b6912b3e

# Checks shared by both package formats, sourced inside the containers.
read -r -d '' common <<'EOF' || true
set -euo pipefail
env_file=/etc/arkeep/agent.env
wants=/etc/systemd/system/multi-user.target.wants/arkeep-agent.service
fail() { echo "FAIL: $*" >&2; exit 1; }
check_installed() {
  [ -x /usr/bin/arkeep-agent ] || fail "binary not installed"
  /usr/bin/arkeep-agent version | grep -q arkeep || fail "binary does not run"
  [ -f /usr/lib/systemd/system/arkeep-agent.service ] || fail "unit not installed"
  grep -qx 'ExecStart=/usr/bin/arkeep-agent --state-dir=/var/lib/arkeep-agent' \
    /usr/lib/systemd/system/arkeep-agent.service.d/package.conf || fail "drop-in missing"
  [ "$(stat -c %a "$env_file")" = 600 ] || fail "agent.env is not 0600"
  [ -L "$wants" ] || fail "service not enabled"
}
configure() {
  sed -i 's|^ARKEEP_SERVER_ADDR=.*|ARKEEP_SERVER_ADDR=arkeep.example.com:9090|' "$env_file"
  mkdir -p /var/lib/arkeep-agent && touch /var/lib/arkeep-agent/agent-state.json
}
check_kept() {
  grep -qx 'ARKEEP_SERVER_ADDR=arkeep.example.com:9090' "$env_file" || fail "agent.env overwritten"
}
EOF

echo "== deb on Debian and Ubuntu"
for image in "$debian" "$ubuntu"; do
  echo "-- ${image%@*}"
  docker run --rm -v "${dist}:/dist:ro" -e DEBIAN_FRONTEND=noninteractive "$image" bash -c "
${common}
apt-get update -qq && apt-get install -y -qq systemd >/dev/null
# A unit left by a manual install is reported.
printf '[Service]\nExecStart=/usr/local/bin/arkeep-agent\n[Install]\nWantedBy=multi-user.target\n' \
  > /etc/systemd/system/arkeep-agent.service
apt-get install -y -qq /dist/${deb} 2>&1 | tee /tmp/install.log >/dev/null
grep -q 'overrides the packaged unit' /tmp/install.log || fail 'manual unit not reported'
grep -q 'set ARKEEP_SERVER_ADDR' /tmp/install.log || fail 'no setup hint on first install'
rm /etc/systemd/system/arkeep-agent.service
check_installed

# Upgrade: the config is kept, the service stays enabled.
configure
dpkg -i /dist/${deb} >/dev/null
check_installed; check_kept

# Upgrade: a service the administrator disabled stays disabled.
systemctl disable arkeep-agent.service 2>/dev/null
dpkg -i /dist/${deb} >/dev/null
[ ! -e \"\$wants\" ] || fail 'upgrade enabled a disabled service'
systemctl enable arkeep-agent.service 2>/dev/null

# Removal: disabled, config and state kept.
apt-get remove -y -qq arkeep-agent >/dev/null
[ ! -e /usr/bin/arkeep-agent ] || fail 'binary left after remove'
[ ! -e \"\$wants\" ] || fail 'service still enabled after remove'
check_kept
[ -f /var/lib/arkeep-agent/agent-state.json ] || fail 'state deleted by remove'

# Purge: config and state deleted.
apt-get purge -y -qq arkeep-agent >/dev/null
[ ! -e \"\$env_file\" ] || fail 'agent.env left after purge'
[ ! -e /var/lib/arkeep-agent ] || fail 'state left after purge'
echo ok
"
done

echo "== rpm on Fedora"
docker run --rm -v "${dist}:/dist:ro" "$fedora" bash -c "
${common}
dnf install -y -q systemd >/dev/null 2>&1
dnf install -y -q /dist/${rpm} 2>&1 | tee /tmp/install.log >/dev/null
grep -q 'set ARKEEP_SERVER_ADDR' /tmp/install.log || fail 'no setup hint on first install'
check_installed

# Upgrade: the config is kept, the service stays enabled.
configure
rpm -U --replacepkgs /dist/${rpm}
check_installed; check_kept

# Upgrade: a service the administrator disabled stays disabled.
systemctl disable arkeep-agent.service 2>/dev/null
rpm -U --replacepkgs /dist/${rpm}
[ ! -e \"\$wants\" ] || fail 'upgrade enabled a disabled service'
systemctl enable arkeep-agent.service 2>/dev/null

# Removal: disabled, state kept, the edited config saved as .rpmsave.
dnf remove -y -q arkeep-agent >/dev/null
[ ! -e /usr/bin/arkeep-agent ] || fail 'binary left after remove'
[ ! -e \"\$wants\" ] || fail 'service still enabled after remove'
grep -qx 'ARKEEP_SERVER_ADDR=arkeep.example.com:9090' \"\${env_file}.rpmsave\" || fail 'edited agent.env lost on remove'
[ -f /var/lib/arkeep-agent/agent-state.json ] || fail 'state deleted by remove'
echo ok
"

echo "All package checks passed."
