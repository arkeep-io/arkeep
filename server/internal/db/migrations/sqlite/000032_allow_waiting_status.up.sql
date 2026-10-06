-- Adds the 'waiting' status: a job queued because one of its destinations is
-- held by another backup or retention sweep (see destinations.busy_job_id,
-- issue #130). It is dispatched automatically once every destination it needs
-- is free (issue #285), instead of being skipped until the next schedule.
--
-- SQLite cannot alter a CHECK constraint; the table must be recreated.
-- PRAGMA foreign_keys = OFF is required because golang-migrate runs this
-- migration without a transaction (NoTxWrap), allowing the PRAGMA to take effect.
-- Child tables (job_destinations, job_logs, job_destination_commands,
-- job_retention_tags) reference jobs by name, so they keep pointing at the
-- recreated table.
PRAGMA foreign_keys = OFF;

CREATE TABLE jobs_new (
    id               TEXT      NOT NULL PRIMARY KEY,
    created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    policy_id        TEXT,
    agent_id         TEXT      NOT NULL,
    status           TEXT      NOT NULL DEFAULT 'pending',
    started_at       TIMESTAMP,
    ended_at         TIMESTAMP,
    error            TEXT      NOT NULL DEFAULT '',
    type             TEXT      NOT NULL DEFAULT 'backup',
    resume_of_job_id TEXT,
    resume_attempt   INTEGER   NOT NULL DEFAULT 0,
    CONSTRAINT fk_jobs_policy    FOREIGN KEY (policy_id) REFERENCES policies     (id) ON DELETE RESTRICT,
    CONSTRAINT fk_jobs_agent     FOREIGN KEY (agent_id)  REFERENCES agents       (id) ON DELETE RESTRICT,
    CONSTRAINT jobs_status_check CHECK (status IN ('pending', 'waiting', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted'))
);
INSERT INTO jobs_new
    SELECT id, created_at, updated_at, policy_id, agent_id, status, started_at, ended_at, error, type, resume_of_job_id, resume_attempt
    FROM jobs;
DROP TABLE jobs;
ALTER TABLE jobs_new RENAME TO jobs;
CREATE INDEX IF NOT EXISTS idx_jobs_policy_id ON jobs (policy_id);
CREATE INDEX IF NOT EXISTS idx_jobs_agent_id  ON jobs (agent_id);
CREATE INDEX IF NOT EXISTS idx_jobs_status    ON jobs (status);

PRAGMA foreign_keys = ON;
