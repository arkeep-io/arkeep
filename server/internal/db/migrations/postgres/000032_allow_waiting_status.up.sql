-- Adds the 'waiting' status: a job queued because one of its destinations is
-- held by another backup or retention sweep (see destinations.busy_job_id,
-- issue #130). It is dispatched automatically once every destination it needs
-- is free (issue #285), instead of being skipped until the next schedule.
ALTER TABLE jobs DROP CONSTRAINT jobs_status_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_status_check CHECK (status IN ('pending', 'waiting', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted'));
