-- Adds the 'interrupted' terminal status: a job whose agent disappeared while it
-- was running (laptop shut down, sleep, network loss) rather than one that
-- genuinely failed. Keeping it distinct from 'failed' is what lets the server
-- resume it on reconnect without retrying real backup errors.
ALTER TABLE jobs DROP CONSTRAINT jobs_status_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted'));

ALTER TABLE job_destinations DROP CONSTRAINT job_destinations_status_check;
ALTER TABLE job_destinations ADD CONSTRAINT job_destinations_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted'));
