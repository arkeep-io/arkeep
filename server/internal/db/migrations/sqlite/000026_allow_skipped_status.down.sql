-- Reverts the 'skipped' status. Existing skipped rows are folded back into
-- 'failed' first, otherwise the narrower CHECK constraint cannot be applied.
PRAGMA foreign_keys = OFF;

UPDATE job_destinations         SET status = 'failed' WHERE status = 'skipped';
UPDATE job_destination_commands SET status = 'failed' WHERE status = 'skipped';

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
    CONSTRAINT job_destinations_status_check   CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted'))
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
    CONSTRAINT job_destination_commands_status_check   CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted'))
);
INSERT INTO job_destination_commands_new
    SELECT id, created_at, updated_at, job_id, destination_id, source_name, status, snapshot_id, size_bytes, started_at, ended_at, error
    FROM job_destination_commands;
DROP TABLE job_destination_commands;
ALTER TABLE job_destination_commands_new RENAME TO job_destination_commands;
CREATE INDEX IF NOT EXISTS idx_job_destination_commands_job_id ON job_destination_commands (job_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_job_destination_commands_job_dest_source ON job_destination_commands (job_id, destination_id, source_name);

PRAGMA foreign_keys = ON;
