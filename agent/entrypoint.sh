#!/bin/sh
set -e

# ── Docker socket group ────────────────────────────────────────────────────────
DOCKER_SOCK="${ARKEEP_DOCKER_SOCKET:-/var/run/docker.sock}"

if [ -S "$DOCKER_SOCK" ]; then
    DOCKER_GID=$(stat -c '%g' "$DOCKER_SOCK")
    if ! getent group "$DOCKER_GID" > /dev/null 2>&1; then
        addgroup -g "$DOCKER_GID" dockerhost
    fi
    DOCKER_GROUP=$(getent group "$DOCKER_GID" | cut -d: -f1)
    # Add to both root and arkeep so either run mode has Docker access.
    addgroup root "$DOCKER_GROUP" 2>/dev/null || true
    addgroup arkeep "$DOCKER_GROUP" 2>/dev/null || true
fi

# ── Run as root (default) or drop to a specific user via PUID/PGID ────────────
# Backing up Docker volumes requires reading files owned by arbitrary UIDs
# (postgres, git, www-data, …). The agent therefore runs as root by default —
# the standard approach for backup agents that need full filesystem access.
#
# To restrict to a specific user (e.g. when backing up only paths you own),
# set PUID (and optionally PGID) in your .env file:
#
#   PUID=1000   # run `id` on the host to find your UID
#   PGID=1000   # optional, defaults to PUID
#
# When PUID is set the agent drops privileges via su-exec. Note that the agent
# will then only be able to read files accessible to that UID.
if [ -n "$PUID" ]; then
    PGID="${PGID:-$PUID}"
    # Remap the arkeep user/group to the requested UID:GID.
    sed -i "s/^arkeep:x:[0-9]*/arkeep:x:${PGID}/" /etc/group
    sed -i "s/^arkeep:x:[0-9]*:[0-9]*/arkeep:x:${PUID}:${PGID}/" /etc/passwd
    chown -R "${PUID}:${PGID}" /var/lib/arkeep-agent 2>/dev/null || true
    exec su-exec arkeep "$@"
fi

# Default: run as root.
exec "$@"
