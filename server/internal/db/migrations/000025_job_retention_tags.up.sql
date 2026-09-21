-- A standalone retention job (JOB_TYPE_FORGET) sweeps every policy's own
-- snapshot-tag pool at one destination in a single run: one `restic forget
-- --prune --tag <tag>` per tag (see agent/internal/restic/wrapper.go's
-- Forget, unchanged). job_destinations still gets exactly one row per
-- retention job, carrying the aggregate result; per-tag detail lives here —
-- same "one job/destination pair, several sub-results" shape as
-- job_destination_commands, but for forget outcomes (no snapshot_id/
-- size_bytes: a forget never creates a snapshot).
CREATE TABLE IF NOT EXISTS job_retention_tags (
    id              TEXT      NOT NULL PRIMARY KEY,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    job_id          TEXT      NOT NULL,
    destination_id  TEXT      NOT NULL,
    tag             TEXT      NOT NULL,
    status          TEXT      NOT NULL DEFAULT 'pending',
    started_at      TIMESTAMP,
    ended_at        TIMESTAMP,
    error           TEXT      NOT NULL DEFAULT '',

    CONSTRAINT fk_job_retention_tags_job         FOREIGN KEY (job_id)         REFERENCES jobs         (id) ON DELETE CASCADE,
    CONSTRAINT fk_job_retention_tags_destination FOREIGN KEY (destination_id) REFERENCES destinations (id) ON DELETE RESTRICT,
    CONSTRAINT job_retention_tags_status_check   CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'skipped'))
);
CREATE INDEX IF NOT EXISTS idx_job_retention_tags_job_id ON job_retention_tags (job_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_job_retention_tags_job_dest_tag ON job_retention_tags (job_id, destination_id, tag);
