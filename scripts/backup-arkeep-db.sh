#!/usr/bin/env bash
# backup-arkeep-db.sh — Back up the Arkeep database and data directory.
#
# Supports three deployment modes (set DEPLOY_MODE below):
#   standalone  — binary/systemd: direct filesystem access (default)
#   docker      — Docker Compose: uses `docker exec` / `docker cp`
#   postgres    — any deploy with external PostgreSQL: uses pg_dump
#
# Usage:
#   ./scripts/backup-arkeep-db.sh
#
# Schedule with cron:
#   echo "0 3 * * * root /usr/local/bin/arkeep-backup" > /etc/cron.d/arkeep-backup
#
# Retention: backups older than KEEP_DAYS days are deleted automatically.
# Helm/Kubernetes: use the CronJob described in docs/operations/backup-recovery.md.

set -euo pipefail

# ── Configuration ─────────────────────────────────────────────────────────────

# Deployment mode: "standalone", "docker", or "postgres"
DEPLOY_MODE="${ARKEEP_DEPLOY_MODE:-standalone}"

# standalone + docker (sqlite): path to the database file inside the container/host
SQLITE_PATH="${ARKEEP_DB_DSN:-/var/lib/arkeep/arkeep.db}"

# docker: name of the server container and data volume mount path inside it
DOCKER_CONTAINER="${ARKEEP_DOCKER_CONTAINER:-arkeep-server}"
DOCKER_DATA_PATH="${ARKEEP_DOCKER_DATA_PATH:-/var/lib/arkeep}"

# postgres: DSN (used in both "postgres" mode and "docker" mode with DB_DRIVER=postgres)
# Example: postgres://arkeep:password@localhost:5432/arkeep
POSTGRES_DSN="${ARKEEP_DB_DSN:-}"

# standalone: data directory path on the host filesystem
DATA_DIR="${ARKEEP_DATA_DIR:-/var/lib/arkeep/data}"

# Where to write backup files (on the host)
BACKUP_DIR="${ARKEEP_BACKUP_DIR:-/var/backups/arkeep}"

# How many days of backups to keep
KEEP_DAYS="${ARKEEP_BACKUP_KEEP_DAYS:-7}"

# ── Helpers ───────────────────────────────────────────────────────────────────

TIMESTAMP=$(date +%Y%m%d-%H%M%S)
log() { echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] $*"; }
die() { log "ERROR: $*" >&2; exit 1; }

# ── Pre-flight ────────────────────────────────────────────────────────────────

mkdir -p "$BACKUP_DIR"
chmod 700 "$BACKUP_DIR"

# ── Database backup ───────────────────────────────────────────────────────────

case "$DEPLOY_MODE" in

  standalone)
    # Direct filesystem access — binary or systemd deployment.
    [[ -f "$SQLITE_PATH" ]] || die "SQLite file not found: $SQLITE_PATH"

    DB_BACKUP="$BACKUP_DIR/arkeep-db-$TIMESTAMP.db"
    log "Backing up SQLite database: $SQLITE_PATH → $DB_BACKUP"

    # .backup uses the SQLite Online Backup API — safe while the server is running
    sqlite3 "$SQLITE_PATH" ".backup '$DB_BACKUP'" \
      || die "sqlite3 backup failed"

    log "Verifying integrity..."
    result=$(sqlite3 "$DB_BACKUP" "PRAGMA integrity_check;")
    [[ "$result" == "ok" ]] || die "Integrity check failed: $result"

    chmod 600 "$DB_BACKUP"
    log "Database backup OK: $DB_BACKUP"

    # Data directory
    if [[ -d "$DATA_DIR" ]]; then
        DATA_BACKUP="$BACKUP_DIR/arkeep-data-$TIMESTAMP.tar.gz"
        log "Backing up data directory: $DATA_DIR → $DATA_BACKUP"
        tar -czf "$DATA_BACKUP" -C "$(dirname "$DATA_DIR")" "$(basename "$DATA_DIR")" \
          || die "tar failed"
        chmod 600 "$DATA_BACKUP"
        log "Data directory backup OK: $DATA_BACKUP"
    else
        log "WARN: data directory not found at $DATA_DIR — skipping"
    fi
    ;;

  docker)
    # Docker Compose deployment.
    # The SQLite file is inside the container volume — use docker exec to reach it.
    command -v docker >/dev/null || die "docker not found in PATH"
    docker inspect "$DOCKER_CONTAINER" >/dev/null 2>&1 \
      || die "Container '$DOCKER_CONTAINER' not found or not running"

    DB_BACKUP="$BACKUP_DIR/arkeep-db-$TIMESTAMP.db"
    log "Backing up SQLite via docker exec ($DOCKER_CONTAINER): $SQLITE_PATH → $DB_BACKUP"

    # Run sqlite3 inside the container, write to a temp path, then copy out
    CONTAINER_TMP="/tmp/arkeep-backup-$TIMESTAMP.db"
    docker exec "$DOCKER_CONTAINER" \
      sqlite3 "$SQLITE_PATH" ".backup '$CONTAINER_TMP'" \
      || die "docker exec sqlite3 backup failed"

    docker cp "$DOCKER_CONTAINER:$CONTAINER_TMP" "$DB_BACKUP" \
      || die "docker cp failed"

    docker exec "$DOCKER_CONTAINER" rm -f "$CONTAINER_TMP"

    log "Verifying integrity..."
    result=$(sqlite3 "$DB_BACKUP" "PRAGMA integrity_check;")
    [[ "$result" == "ok" ]] || die "Integrity check failed: $result"

    chmod 600 "$DB_BACKUP"
    log "Database backup OK: $DB_BACKUP"

    # Data directory — tar inside the container, copy out
    DATA_BACKUP="$BACKUP_DIR/arkeep-data-$TIMESTAMP.tar.gz"
    log "Backing up data directory via docker exec → $DATA_BACKUP"

    CONTAINER_DATA_TMP="/tmp/arkeep-data-$TIMESTAMP.tar.gz"
    docker exec "$DOCKER_CONTAINER" \
      tar -czf "$CONTAINER_DATA_TMP" -C "$DOCKER_DATA_PATH" data/ \
      || die "docker exec tar failed"

    docker cp "$DOCKER_CONTAINER:$CONTAINER_DATA_TMP" "$DATA_BACKUP" \
      || die "docker cp data failed"

    docker exec "$DOCKER_CONTAINER" rm -f "$CONTAINER_DATA_TMP"

    chmod 600 "$DATA_BACKUP"
    log "Data directory backup OK: $DATA_BACKUP"
    ;;

  postgres)
    # External PostgreSQL — any deployment type.
    [[ -n "$POSTGRES_DSN" ]] || die "ARKEEP_DB_DSN must be set for postgres mode"
    command -v pg_dump >/dev/null || die "pg_dump not found in PATH"

    DB_BACKUP="$BACKUP_DIR/arkeep-db-$TIMESTAMP.dump"
    log "Backing up PostgreSQL database → $DB_BACKUP"

    pg_dump -Fc "$POSTGRES_DSN" -f "$DB_BACKUP" \
      || die "pg_dump failed"

    chmod 600 "$DB_BACKUP"
    log "Database backup OK: $DB_BACKUP"

    # Data directory — only relevant for standalone; Docker/Helm handle it differently
    if [[ -d "$DATA_DIR" ]]; then
        DATA_BACKUP="$BACKUP_DIR/arkeep-data-$TIMESTAMP.tar.gz"
        log "Backing up data directory: $DATA_DIR → $DATA_BACKUP"
        tar -czf "$DATA_BACKUP" -C "$(dirname "$DATA_DIR")" "$(basename "$DATA_DIR")" \
          || die "tar failed"
        chmod 600 "$DATA_BACKUP"
        log "Data directory backup OK: $DATA_BACKUP"
    else
        log "WARN: data directory not found at $DATA_DIR — skipping"
    fi
    ;;

  *)
    die "Unknown DEPLOY_MODE: $DEPLOY_MODE (expected 'standalone', 'docker', or 'postgres')"
    ;;

esac

# ── Retention cleanup ─────────────────────────────────────────────────────────

log "Removing backups older than $KEEP_DAYS days..."
find "$BACKUP_DIR" -maxdepth 1 \
  \( -name "arkeep-db-*.db" -o -name "arkeep-db-*.dump" -o -name "arkeep-data-*.tar.gz" \) \
  -mtime "+$KEEP_DAYS" -print -delete

# ── Done ──────────────────────────────────────────────────────────────────────

log "Backup completed successfully."
log "Files in $BACKUP_DIR:"
ls -lh "$BACKUP_DIR" | grep "arkeep-" || true
