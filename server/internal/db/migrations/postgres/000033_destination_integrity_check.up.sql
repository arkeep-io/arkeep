-- Scheduled repository integrity checks (restic check) per destination
-- (issue #307). Run by the destination's retention agent, which now acts as
-- its maintenance agent for both retention and check.
ALTER TABLE destinations ADD COLUMN check_enabled BOOLEAN NOT NULL DEFAULT FALSE;
-- Cron expression for the check; '' = not configured/scheduled.
ALTER TABLE destinations ADD COLUMN check_schedule TEXT NOT NULL DEFAULT '';
-- 'structure' (metadata only), 'subset' (read check_subset_percent of the
-- pack data) or 'full' (read all pack data).
ALTER TABLE destinations ADD COLUMN check_mode TEXT NOT NULL DEFAULT 'subset';
ALTER TABLE destinations ADD COLUMN check_subset_percent INTEGER NOT NULL DEFAULT 5;
-- Outcome of the most recent check job. No FK on the job id: job-log
-- retention may delete the job while its outcome stays relevant.
ALTER TABLE destinations ADD COLUMN last_check_at TIMESTAMP;
ALTER TABLE destinations ADD COLUMN last_check_status TEXT NOT NULL DEFAULT '';
ALTER TABLE destinations ADD COLUMN last_check_job_id TEXT;

-- Weekly subset check by default, but only where a maintenance agent is
-- already assigned: without one the check could never run.
UPDATE destinations SET check_enabled = TRUE, check_schedule = '0 3 * * 0'
WHERE retention_agent_id IS NOT NULL AND deleted_at IS NULL;
