#!/bin/sh
set -e

# ── PUID / PGID remapping ──────────────────────────────────────────────────────
# Set PUID and PGID to match the UID/GID that owns your bind-mounted backup
# directories on the host. This lets the agent write to host paths without
# requiring manual chown on the host.
#
#   Example .env:
#     PUID=1000
#     PGID=1000   # optional, defaults to PUID
#
# To find your host UID/GID: run `id` on the host machine.
if [ -n "$PUID" ]; then
    PGID="${PGID:-$PUID}"
    # Remap the arkeep group to the requested GID.
    sed -i "s/^arkeep:x:[0-9]*/arkeep:x:${PGID}/" /etc/group
    # Remap the arkeep user to the requested UID:GID.
    sed -i "s/^arkeep:x:[0-9]*:[0-9]*/arkeep:x:${PUID}:${PGID}/" /etc/passwd
    # Fix ownership of the state dir so the remapped user can still access it.
    chown -R "${PUID}:${PGID}" /var/lib/arkeep-agent 2>/dev/null || true
fi

# ── Docker socket group ────────────────────────────────────────────────────────
DOCKER_SOCK="${ARKEEP_DOCKER_SOCKET:-/var/run/docker.sock}"

if [ -S "$DOCKER_SOCK" ]; then
    DOCKER_GID=$(stat -c '%g' "$DOCKER_SOCK")
    if ! getent group "$DOCKER_GID" > /dev/null 2>&1; then
        addgroup -g "$DOCKER_GID" dockerhost
    fi
    DOCKER_GROUP=$(getent group "$DOCKER_GID" | cut -d: -f1)
    addgroup arkeep "$DOCKER_GROUP"
fi

exec su-exec arkeep "$@"
