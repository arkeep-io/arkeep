#!/bin/sh
set -e

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
