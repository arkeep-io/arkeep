# Arkeep — Backup & Disaster Recovery

This guide covers how to back up the Arkeep server itself and how to recover
from common failure scenarios. It is distinct from the backups Arkeep manages
for your machines — this is about protecting the Arkeep installation.

---

## What needs to be backed up

| Component | Location | Why it matters |
|---|---|---|
| **Database** | `--db-dsn` (SQLite file or PostgreSQL) | All configuration: policies, agents, destinations, users, jobs, audit log |
| **Data directory** | `--data-dir` | Private CA key + server/client TLS certificates. Losing this requires re-enrolling all agents |
| **Secret key** | `ARKEEP_SECRET_KEY` env var | AES-256 key used to encrypt stored credentials (destination passwords, SMTP password, webhook secret). Losing this makes all encrypted values unreadable |

> **Your actual backup data** (Restic repositories) lives on the destinations you
> configured (S3, SFTP, local path, etc.) and is completely independent of the
> Arkeep server. A total server loss does not affect Restic repository integrity.

---

## SQLite

### Scheduled backup (recommended)

Use the provided script ([scripts/backup-arkeep-db.sh](../../scripts/backup-arkeep-db.sh))
for regular automated backups. Set `ARKEEP_DEPLOY_MODE` to match your installation:

| Deployment | `ARKEEP_DEPLOY_MODE` |
|---|---|
| Binary / systemd | `standalone` (default) |
| Docker Compose | `docker` |
| External PostgreSQL | `postgres` |
| Helm / Kubernetes | use the CronJob in the [Helm section](#helm--kubernetes) below |

```bash
# Copy the script
sudo cp scripts/backup-arkeep-db.sh /usr/local/bin/arkeep-backup
sudo chmod +x /usr/local/bin/arkeep-backup

# Edit paths at the top of the script to match your installation
sudo nano /usr/local/bin/arkeep-backup

# Test it manually first
sudo /usr/local/bin/arkeep-backup

# Add a daily cron job (runs at 03:00)
echo "0 3 * * * root /usr/local/bin/arkeep-backup" | sudo tee /etc/cron.d/arkeep-backup
```

### Manual one-off backup

```bash
# Safe to run while the server is running (SQLite WAL mode)
sqlite3 /var/lib/arkeep/arkeep.db \
  ".backup '/var/backups/arkeep/arkeep-$(date +%Y%m%d-%H%M%S).db'"

# Verify the backup is not corrupt
sqlite3 /var/backups/arkeep/arkeep-YYYYMMDD-HHMMSS.db "PRAGMA integrity_check;"
# Expected output: ok
```

### Docker Compose

Use the script with `ARKEEP_DEPLOY_MODE=docker` — it handles `docker exec` and `docker cp`
automatically:

```bash
ARKEEP_DEPLOY_MODE=docker \
ARKEEP_DOCKER_CONTAINER=arkeep-server \
ARKEEP_DB_DSN=/var/lib/arkeep/arkeep.db \
  /usr/local/bin/arkeep-backup
```

Or schedule it in cron:

```bash
cat <<'EOF' | sudo tee /etc/cron.d/arkeep-backup
ARKEEP_DEPLOY_MODE=docker
ARKEEP_DOCKER_CONTAINER=arkeep-server
ARKEEP_DB_DSN=/var/lib/arkeep/arkeep.db
ARKEEP_BACKUP_DIR=/var/backups/arkeep
0 3 * * * root /usr/local/bin/arkeep-backup
EOF
```

Manual one-off (without the script):

```bash
docker exec arkeep-server \
  sqlite3 /var/lib/arkeep/arkeep.db \
  ".backup '/tmp/arkeep-backup.db'"
docker cp arkeep-server:/tmp/arkeep-backup.db ./arkeep-$(date +%Y%m%d-%H%M%S).db
docker exec arkeep-server rm /tmp/arkeep-backup.db
```

---

## Standalone binary / systemd

Default paths when running the server binary directly (adjust to your installation):

| Item | Default path |
|---|---|
| SQLite database | `./arkeep.db` (or whatever `--db-dsn` is set to) |
| Data directory | `./data` (or whatever `--data-dir` is set to) |

### Database backup

```bash
# SQLite — safe to run while the server is running
sqlite3 /var/lib/arkeep/arkeep.db \
  ".backup '/var/backups/arkeep/arkeep-$(date +%Y%m%d-%H%M%S).db'"

sqlite3 /var/backups/arkeep/arkeep-YYYYMMDD-HHMMSS.db "PRAGMA integrity_check;"
```

### Data directory backup

```bash
tar -czf /var/backups/arkeep/arkeep-data-$(date +%Y%m%d-%H%M%S).tar.gz \
  -C /var/lib/arkeep data/
chmod 600 /var/backups/arkeep/arkeep-data-*.tar.gz
```

### Automated backup with the provided script

```bash
sudo cp scripts/backup-arkeep-db.sh /usr/local/bin/arkeep-backup
sudo chmod +x /usr/local/bin/arkeep-backup

# Edit paths at the top to match your installation
sudo nano /usr/local/bin/arkeep-backup

# Schedule daily at 03:00
echo "0 3 * * * root /usr/local/bin/arkeep-backup" | sudo tee /etc/cron.d/arkeep-backup
```

The script defaults to `ARKEEP_DEPLOY_MODE=standalone`. For Docker Compose, set
`ARKEEP_DEPLOY_MODE=docker` (see [Docker Compose](#docker-compose) below).

---

## PostgreSQL

### Using the script (recommended)

Use `ARKEEP_DEPLOY_MODE=postgres` with the provided script. If `pg_dump` is not
installed on the host (e.g. Windows, or a server without the PostgreSQL client),
set `ARKEEP_POSTGRES_CONTAINER` to run `pg_dump` inside the database container:

```bash
# pg_dump available locally
ARKEEP_DEPLOY_MODE=postgres \
ARKEEP_DB_DSN="postgres://arkeep:password@localhost:5432/arkeep" \
ARKEEP_BACKUP_DIR=/var/backups/arkeep \
  /usr/local/bin/arkeep-backup

# pg_dump not installed locally — run via docker exec (Docker Compose or Windows)
ARKEEP_DEPLOY_MODE=postgres \
ARKEEP_DB_DSN="postgres://arkeep:password@localhost:5432/arkeep" \
ARKEEP_POSTGRES_CONTAINER=arkeep-postgres \
ARKEEP_BACKUP_DIR=/var/backups/arkeep \
  /usr/local/bin/arkeep-backup
```

Schedule with cron:

```bash
cat <<'EOF' | sudo tee /etc/cron.d/arkeep-backup
ARKEEP_DEPLOY_MODE=postgres
ARKEEP_DB_DSN=postgres://arkeep:password@localhost:5432/arkeep
ARKEEP_POSTGRES_CONTAINER=arkeep-postgres
ARKEEP_BACKUP_DIR=/var/backups/arkeep
0 3 * * * root /usr/local/bin/arkeep-backup
EOF
```

> **Windows (Git Bash):** the script automatically detects Git Bash (`$OSTYPE=msys`)
> and applies two workarounds: skips `chmod` (no-op on NTFS) and sets
> `MSYS_NO_PATHCONV=1` on `docker` calls to prevent path translation inside containers.

### Manual backup (without the script)

```bash
# Custom format (compressed, most flexible for restore)
pg_dump -Fc -d arkeep -U arkeep -h localhost \
  -f /var/backups/arkeep/arkeep-$(date +%Y%m%d-%H%M%S).dump

# Plain SQL (human-readable, slower restore)
pg_dump -d arkeep -U arkeep -h localhost \
  -f /var/backups/arkeep/arkeep-$(date +%Y%m%d-%H%M%S).sql
```

### Managed databases

If you use a managed PostgreSQL service (AWS RDS, Google Cloud SQL, Supabase, etc.),
enable automated backups in the provider console — this supersedes manual `pg_dump`.
Keep at least 7 days of point-in-time recovery (PITR) enabled.

---

## Data directory

The `--data-dir` directory contains the private CA key and all TLS certificates.
Back it up alongside the database.

```bash
tar -czf /var/backups/arkeep/arkeep-data-$(date +%Y%m%d-%H%M%S).tar.gz \
  -C /var/lib/arkeep data/

# Restrict permissions — this archive contains private keys
chmod 600 /var/backups/arkeep/arkeep-data-*.tar.gz
```

---

## Helm / Kubernetes

The Helm chart stores data in a `PersistentVolumeClaim` mounted at `/var/lib/arkeep`
(configurable via `persistence.mountPath` in `values.yaml`).

### SQLite on a PersistentVolume

```bash
# One-off backup via kubectl exec (server pod must be running)
POD=$(kubectl get pod -l app.kubernetes.io/name=arkeep -o jsonpath='{.items[0].metadata.name}')

kubectl exec "$POD" -- \
  sqlite3 /var/lib/arkeep/arkeep.db \
  ".backup '/var/lib/arkeep/arkeep-$(date +%Y%m%d-%H%M%S).db'"

# Copy the backup file out of the cluster
kubectl cp "$POD:/var/lib/arkeep/arkeep-YYYYMMDD-HHMMSS.db" ./arkeep-backup.db
```

For scheduled backups, use a `CronJob` that runs in the same namespace and mounts
the same PVC:

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: arkeep-db-backup
spec:
  schedule: "0 3 * * *"
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - name: backup
              image: keinos/sqlite3:latest
              command:
                - sh
                - -c
                - |
                  sqlite3 /data/arkeep.db ".backup '/data/backups/arkeep-$(date +%Y%m%d-%H%M%S).db'"
                  find /data/backups -name "arkeep-*.db" -mtime +7 -delete
              volumeMounts:
                - name: data
                  mountPath: /data
          restartPolicy: OnFailure
          volumes:
            - name: data
              persistentVolumeClaim:
                claimName: arkeep-data   # adjust to your release name
```

### PostgreSQL (bundled chart)

When `postgresql.enabled=true` the chart deploys Bitnami PostgreSQL. Back it up with
`pg_dump` from a temporary pod:

```bash
kubectl run pg-backup --rm -it --restart=Never \
  --image=postgres:16 \
  --env="PGPASSWORD=<your-postgres-password>" \
  -- pg_dump -h arkeep-postgresql -U arkeep -Fc arkeep \
     -f /tmp/arkeep-$(date +%Y%m%d).dump

# Copy out of the pod before it's deleted
kubectl cp pg-backup:/tmp/arkeep-YYYYMMDD.dump ./arkeep-YYYYMMDD.dump
```

For production use, enable Bitnami PostgreSQL's built-in backup via
`postgresql.primary.persistence` and a `VolumeSnapshot`, or use a managed
PostgreSQL service and let the provider handle PITR.

### Data directory (PVC)

The data directory (CA key + TLS certificates) is on the same PVC as the database.
The `kubectl exec` + `kubectl cp` approach above captures it as part of the same volume.
If you use `VolumeSnapshot`, one snapshot covers both.

```bash
# Explicit tar of the data sub-directory
kubectl exec "$POD" -- \
  tar -czf /var/lib/arkeep/arkeep-data-$(date +%Y%m%d-%H%M%S).tar.gz \
  -C /var/lib/arkeep data/

kubectl cp "$POD:/var/lib/arkeep/arkeep-data-YYYYMMDD-HHMMSS.tar.gz" \
  ./arkeep-data-backup.tar.gz
```

### VolumeSnapshot (recommended for production)

If your storage class supports `VolumeSnapshot`, this is the simplest approach:

```bash
kubectl apply -f - <<EOF
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: arkeep-data-$(date +%Y%m%d-%H%M%S)
spec:
  volumeSnapshotClassName: csi-hostpath-snapclass   # adjust to your cluster
  source:
    persistentVolumeClaimName: arkeep-data          # adjust to your release name
EOF
```

---

## Secret key

The `ARKEEP_SECRET_KEY` is not stored on disk by Arkeep — it is injected via
environment variable at startup. Store it in a password manager or secrets vault
(Vault, AWS Secrets Manager, 1Password, etc.) separately from the database backup.

> If you lose the secret key, all credentials stored in the database
> (destination passwords, SMTP password, webhook secret) become unreadable.
> You would need to re-enter them after recovery.

---

## Suggested RPO and RTO

| Target | Suggested approach |
|---|---|
| **RPO 24 h** | Daily cron backup of database + data directory |
| **RPO 1 h** | PostgreSQL streaming replication or managed PITR |
| **RTO 30 min** | Follow the [Recovery procedure](#recovery-procedure) below with a tested backup |
| **RTO 5 min** | Hot standby with PostgreSQL streaming replication |

---

## Recovery procedure

### Scenario 1 — Server host lost, database intact on another volume

1. Install Arkeep on the new host (same version — check `CHANGELOG.md` for upgrade notes if versions differ).
2. Restore the data directory:
   ```bash
   tar -xzf arkeep-data-YYYYMMDD-HHMMSS.tar.gz -C /var/lib/arkeep/
   ```
3. Point `--db-dsn` at the existing database (SQLite file or PostgreSQL DSN).
4. Set `ARKEEP_SECRET_KEY` to the original value from your secrets vault.
5. Start the server — it will reuse the existing CA and certificates.
6. Agents will reconnect automatically on their next retry cycle (default: within 30 seconds).

### Scenario 2 — Database lost, data directory intact

1. Restore the database from the latest backup:

   **SQLite:**
   ```bash
   cp /var/backups/arkeep/arkeep-YYYYMMDD-HHMMSS.db /var/lib/arkeep/arkeep.db
   sqlite3 /var/lib/arkeep/arkeep.db "PRAGMA integrity_check;"
   ```

   **PostgreSQL:**
   ```bash
   createdb -U postgres arkeep
   pg_restore -Fc -d arkeep -U postgres arkeep-YYYYMMDD-HHMMSS.dump
   ```

2. Start the server with the original `ARKEEP_SECRET_KEY`.
3. Verify the restored data is consistent at `GET /health/ready` and by logging in.
4. Agents will reconnect automatically — their certificates in `--state-dir` are still valid.

### Scenario 3 — Data directory lost (CA and certificates gone)

This is the most disruptive scenario. Agents cannot reconnect because the CA that
issued their certificates no longer exists.

1. Restore the database from backup.
2. Start the server **without** a data directory — it will generate a new CA on startup.
3. Re-enroll each agent:
   ```bash
   # On each agent machine: delete the old state and restart
   rm -rf /var/lib/arkeep-agent/certs/
   systemctl restart arkeep-agent
   # The agent will re-enroll automatically on startup
   ```
4. All policies, jobs, and configuration in the database are intact.

### Scenario 4 — Full loss (database + data directory)

1. Restore both the database and the data directory from backup (Scenarios 1+2).
2. If no database backup is available: start fresh. You will need to recreate
   policies, users, and destinations manually.
   - **Your Restic repositories are unaffected** — backup data is on the destinations
     you configured and accessible directly via the `restic` CLI at any time.

---

## Verifying backups

Never assume a backup is valid without testing it. Run this check weekly or after
every backup:

```bash
# SQLite
sqlite3 /var/backups/arkeep/arkeep-latest.db "PRAGMA integrity_check;"

# PostgreSQL — restore to a temporary database
createdb -U postgres arkeep_verify
pg_restore -Fc -d arkeep_verify -U postgres arkeep-latest.dump
psql -U postgres -d arkeep_verify -c "SELECT COUNT(*) FROM users;"
dropdb -U postgres arkeep_verify
```

---

## Checklist before upgrading

- [ ] Back up the database (`scripts/backup-arkeep-db.sh` or `pg_dump`)
- [ ] Back up the data directory (`tar -czf ...`)
- [ ] Note the current version (`GET /api/v1/version` or `arkeep-server --version`)
- [ ] Pull the new image / binary
- [ ] Restart the server and verify `GET /health/ready` returns healthy
