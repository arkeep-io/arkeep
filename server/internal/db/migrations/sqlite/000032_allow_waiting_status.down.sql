-- Reverts the 'waiting' status. Queued jobs never ran, so they are folded into
-- 'failed' first, otherwise the narrower CHECK constraint cannot be applied.
--
-- SQLite cannot alter a CHECK constraint; the table must be recreated.
-- PRAGMA foreign_keys = OFF is required because golang-migrate runs this
-- migration without a transaction (NoTxWrap), allowing the PRAGMA to take effect.
-- Child tables (job_destinations, job_logs, job_destination_commands,
-- job_retention_tags) reference jobs by name, so they keep pointing at the
-- recreated table.
PRAGMA foreign_keys = OFF;

UPDATE jobs SET status = 'failed', error = 'queue removed by migration rollback' WHERE status = 'waiting';

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
    CONSTRAINT jobs_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted'))
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
