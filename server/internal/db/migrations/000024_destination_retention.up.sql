-- Retention moves from being a per-Policy setting run inline after every
-- backup, to a per-Destination setting run on its own independent schedule
-- (issue #130). One retention configuration applies uniformly to every
-- policy's own snapshot-tag pool at that destination.
ALTER TABLE destinations ADD COLUMN retention_last INTEGER NOT NULL DEFAULT 0;
ALTER TABLE destinations ADD COLUMN retention_hourly INTEGER NOT NULL DEFAULT 0;
ALTER TABLE destinations ADD COLUMN retention_daily INTEGER NOT NULL DEFAULT 0;
ALTER TABLE destinations ADD COLUMN retention_weekly INTEGER NOT NULL DEFAULT 0;
ALTER TABLE destinations ADD COLUMN retention_monthly INTEGER NOT NULL DEFAULT 0;
ALTER TABLE destinations ADD COLUMN retention_yearly INTEGER NOT NULL DEFAULT 0;
-- Cron expression for the retention sweep; '' = not configured/scheduled.
ALTER TABLE destinations ADD COLUMN retention_schedule TEXT NOT NULL DEFAULT '';
ALTER TABLE destinations ADD COLUMN retention_enabled INTEGER NOT NULL DEFAULT 0;
-- The agent responsible for running this destination's retention sweeps.
-- Nullable (unlike policies.agent_id, which is required) — a destination may
-- have no retention agent assigned yet. No FK, matching this project's
-- convention for other nullable uuid references (e.g. jobs.resume_of_job_id).
ALTER TABLE destinations ADD COLUMN retention_agent_id TEXT;
-- Repositories that can never support `restic forget --prune` by design
-- (WORM/object-lock storage). The retention scheduler must never attempt a
-- sweep here.
ALTER TABLE destinations ADD COLUMN append_only INTEGER NOT NULL DEFAULT 0;
-- Set by the one-time backfill (server/cmd/server/main.go) when this
-- destination was shared by 2+ policies with no single retention
-- configuration to inherit unambiguously. Cleared the first time an admin
-- explicitly saves retention config for this destination.
ALTER TABLE destinations ADD COLUMN retention_needs_review INTEGER NOT NULL DEFAULT 0;

-- Busy gate: which job (backup or retention, any agent) currently holds this
-- destination's repository, so the server can skip a second dispatch instead
-- of racing restic's own lock file. No FK — the holding job may later be
-- deleted by job-log retention.
ALTER TABLE destinations ADD COLUMN busy_job_id TEXT;
ALTER TABLE destinations ADD COLUMN busy_since TIMESTAMP;

CREATE INDEX IF NOT EXISTS idx_destinations_retention_agent_id ON destinations (retention_agent_id);
