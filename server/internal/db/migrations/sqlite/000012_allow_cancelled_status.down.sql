PRAGMA foreign_keys = OFF;

DELETE FROM job_destinations WHERE status = 'cancelled';
DELETE FROM jobs WHERE status = 'cancelled';

CREATE TABLE jobs_new (
    id          TEXT      NOT NULL PRIMARY KEY,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    policy_id   TEXT      NOT NULL,
    agent_id    TEXT      NOT NULL,
    status      TEXT      NOT NULL DEFAULT 'pending',
    started_at  TIMESTAMP,
    ended_at    TIMESTAMP,
    error       TEXT      NOT NULL DEFAULT '',
    type        TEXT      NOT NULL DEFAULT 'backup',
    CONSTRAINT fk_jobs_policy    FOREIGN KEY (policy_id) REFERENCES policies     (id) ON DELETE RESTRICT,
    CONSTRAINT fk_jobs_agent     FOREIGN KEY (agent_id)  REFERENCES agents       (id) ON DELETE RESTRICT,
    CONSTRAINT jobs_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed'))
);
INSERT INTO jobs_new
    SELECT id, created_at, updated_at, policy_id, agent_id, status, started_at, ended_at, error, type
    FROM jobs;
DROP TABLE jobs;
ALTER TABLE jobs_new RENAME TO jobs;
CREATE INDEX IF NOT EXISTS idx_jobs_policy_id ON jobs (policy_id);
CREATE INDEX IF NOT EXISTS idx_jobs_agent_id  ON jobs (agent_id);
CREATE INDEX IF NOT EXISTS idx_jobs_status    ON jobs (status);

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
    CONSTRAINT job_destinations_status_check   CHECK (status IN ('pending', 'running', 'succeeded', 'failed'))
);
INSERT INTO job_destinations_new
    SELECT id, created_at, updated_at, job_id, destination_id, status, snapshot_id, size_bytes, started_at, ended_at, error
    FROM job_destinations;
DROP TABLE job_destinations;
ALTER TABLE job_destinations_new RENAME TO job_destinations;
CREATE INDEX IF NOT EXISTS idx_job_destinations_job_id         ON job_destinations (job_id);
CREATE INDEX IF NOT EXISTS idx_job_destinations_destination_id ON job_destinations (destination_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_job_destinations_job_dest ON job_destinations (job_id, destination_id);

DROP INDEX IF EXISTS uq_policy_destinations_policy_dest;

PRAGMA foreign_keys = ON;
