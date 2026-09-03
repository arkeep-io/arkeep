-- A "command" source is backed up by its own restic invocation
-- (restic backup --stdin-from-command), so one (job, destination) pair can
-- now produce several snapshots: at most one for the regular directory /
-- docker-volume sources, plus one per command source. job_destinations keeps
-- its UNIQUE(job_id, destination_id) shape and its single-snapshot meaning
-- untouched; per-command-source results live here instead.

CREATE TABLE IF NOT EXISTS job_destination_commands (
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

CREATE INDEX IF NOT EXISTS idx_job_destination_commands_job_id ON job_destination_commands (job_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_job_destination_commands_job_dest_source ON job_destination_commands (job_id, destination_id, source_name);
