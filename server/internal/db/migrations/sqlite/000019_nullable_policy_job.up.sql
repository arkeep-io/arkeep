-- Snapshots imported from a pre-existing Restic repository belong to a
-- destination but to no policy and no job: they were not produced by a backup
-- run. The same applies to a restore job started from such a snapshot, which
-- has no policy either. Both cases need policy_id / job_id to accept NULL.
--
-- SQLite cannot drop a NOT NULL constraint, so the tables must be recreated.
-- PRAGMA foreign_keys = OFF is required because golang-migrate runs this
-- migration without a transaction (NoTxWrap), allowing the PRAGMA to take
-- effect. The foreign keys themselves are kept: in SQL a foreign key accepts
-- NULL, so referential integrity still holds for non-imported rows.
PRAGMA foreign_keys = OFF;

CREATE TABLE snapshots_new (
    id              TEXT      NOT NULL PRIMARY KEY,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    policy_id       TEXT,
    destination_id  TEXT      NOT NULL,
    job_id          TEXT,
    snapshot_id     TEXT      NOT NULL,
    size_bytes      INTEGER   NOT NULL DEFAULT 0,
    file_count      INTEGER   NOT NULL DEFAULT 0,
    tags            TEXT      NOT NULL DEFAULT '[]',
    snapshot_at     TIMESTAMP NOT NULL,
    sources         TEXT      NOT NULL DEFAULT '[]',
    is_imported     BOOLEAN   NOT NULL DEFAULT FALSE,
    hostname        TEXT      NOT NULL DEFAULT '',
    CONSTRAINT fk_snapshots_policy      FOREIGN KEY (policy_id)      REFERENCES policies     (id) ON DELETE RESTRICT,
    CONSTRAINT fk_snapshots_destination FOREIGN KEY (destination_id) REFERENCES destinations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_snapshots_job         FOREIGN KEY (job_id)         REFERENCES jobs         (id) ON DELETE RESTRICT
);
INSERT INTO snapshots_new
    SELECT id, created_at, updated_at, policy_id, destination_id, job_id, snapshot_id,
           size_bytes, file_count, tags, snapshot_at, sources, is_imported, hostname
    FROM snapshots;
DROP TABLE snapshots;
ALTER TABLE snapshots_new RENAME TO snapshots;
CREATE INDEX IF NOT EXISTS idx_snapshots_policy_id      ON snapshots (policy_id);
CREATE INDEX IF NOT EXISTS idx_snapshots_destination_id ON snapshots (destination_id);
CREATE INDEX IF NOT EXISTS idx_snapshots_job_id         ON snapshots (job_id);
CREATE INDEX IF NOT EXISTS idx_snapshots_snapshot_id    ON snapshots (snapshot_id);
CREATE INDEX IF NOT EXISTS idx_snapshots_snapshot_at    ON snapshots (snapshot_at);

CREATE TABLE jobs_new (
    id          TEXT      NOT NULL PRIMARY KEY,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    policy_id   TEXT,
    agent_id    TEXT      NOT NULL,
    status      TEXT      NOT NULL DEFAULT 'pending',
    started_at  TIMESTAMP,
    ended_at    TIMESTAMP,
    error       TEXT      NOT NULL DEFAULT '',
    type        TEXT      NOT NULL DEFAULT 'backup',
    CONSTRAINT fk_jobs_policy    FOREIGN KEY (policy_id) REFERENCES policies     (id) ON DELETE RESTRICT,
    CONSTRAINT fk_jobs_agent     FOREIGN KEY (agent_id)  REFERENCES agents       (id) ON DELETE RESTRICT,
    CONSTRAINT jobs_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled'))
);
INSERT INTO jobs_new
    SELECT id, created_at, updated_at, policy_id, agent_id, status, started_at, ended_at, error, type
    FROM jobs;
DROP TABLE jobs;
ALTER TABLE jobs_new RENAME TO jobs;
CREATE INDEX IF NOT EXISTS idx_jobs_policy_id ON jobs (policy_id);
CREATE INDEX IF NOT EXISTS idx_jobs_agent_id  ON jobs (agent_id);
CREATE INDEX IF NOT EXISTS idx_jobs_status    ON jobs (status);

PRAGMA foreign_keys = ON;
