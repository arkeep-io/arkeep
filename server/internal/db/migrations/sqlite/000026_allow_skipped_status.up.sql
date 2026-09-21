-- Adds the 'skipped' status: a destination excluded from a job's run because
-- the server's busy-gate found another operation (backup or retention)
-- already in progress against that same repository (see destinations.
-- busy_job_id, issue #130). Kept distinct from 'failed' so a routine,
-- expected deferral doesn't read as an error.
--
-- SQLite cannot alter a CHECK constraint; the tables must be recreated.
-- PRAGMA foreign_keys = OFF is required because golang-migrate runs this
-- migration without a transaction (NoTxWrap), allowing the PRAGMA to take effect.
PRAGMA foreign_keys = OFF;

CREATE TABLE job_destinations_new (
    id              TEXT      NOT NULL PRIMARY KEY,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    job_id          TEXT      NOT NULL,
    destination_id  TEXT      NOT NULL,
    status          TEXT      NOT NULL DEFAULT 'pending',
    snapshot_id     TEXT      NOT NULL DEFAULT '',
    size_bytes      INTEGER   NOT NULL DEFAULT 0,
    started_at      TIMESTAMP,
    ended_at        TIMESTAMP,
    error           TEXT      NOT NULL DEFAULT '',
    CONSTRAINT fk_job_destinations_job         FOREIGN KEY (job_id)         REFERENCES jobs         (id) ON DELETE CASCADE,
    CONSTRAINT fk_job_destinations_destination FOREIGN KEY (destination_id) REFERENCES destinations (id) ON DELETE RESTRICT,
    CONSTRAINT job_destinations_status_check   CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted', 'skipped'))
);
INSERT INTO job_destinations_new
    SELECT id, created_at, updated_at, job_id, destination_id, status, snapshot_id, size_bytes, started_at, ended_at, error
    FROM job_destinations;
DROP TABLE job_destinations;
ALTER TABLE job_destinations_new RENAME TO job_destinations;
CREATE INDEX IF NOT EXISTS idx_job_destinations_job_id         ON job_destinations (job_id);
CREATE INDEX IF NOT EXISTS idx_job_destinations_destination_id ON job_destinations (destination_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_job_destinations_job_dest ON job_destinations (job_id, destination_id);

CREATE TABLE job_destination_commands_new (
    id              TEXT      NOT NULL PRIMARY KEY,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    job_id          TEXT      NOT NULL,
    destination_id  TEXT      NOT NULL,
    source_name     TEXT      NOT NULL,
    status          TEXT      NOT NULL DEFAULT 'pending',
    snapshot_id     TEXT      NOT NULL DEFAULT '',
    size_bytes      BIGINT    NOT NULL DEFAULT 0,
    started_at      TIMESTAMP,
    ended_at        TIMESTAMP,
    error           TEXT      NOT NULL DEFAULT '',
    CONSTRAINT fk_job_destination_commands_job         FOREIGN KEY (job_id)         REFERENCES jobs         (id) ON DELETE CASCADE,
    CONSTRAINT fk_job_destination_commands_destination FOREIGN KEY (destination_id) REFERENCES destinations (id) ON DELETE RESTRICT,
    CONSTRAINT job_destination_commands_status_check   CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted', 'skipped'))
);
INSERT INTO job_destination_commands_new
    SELECT id, created_at, updated_at, job_id, destination_id, source_name, status, snapshot_id, size_bytes, started_at, ended_at, error
    FROM job_destination_commands;
DROP TABLE job_destination_commands;
ALTER TABLE job_destination_commands_new RENAME TO job_destination_commands;
CREATE INDEX IF NOT EXISTS idx_job_destination_commands_job_id ON job_destination_commands (job_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_job_destination_commands_job_dest_source ON job_destination_commands (job_id, destination_id, source_name);

PRAGMA foreign_keys = ON;
