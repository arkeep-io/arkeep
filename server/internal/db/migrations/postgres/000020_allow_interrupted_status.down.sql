-- Reverts the 'interrupted' status. Existing interrupted rows are folded back
-- into 'failed' first, otherwise the narrower CHECK constraint cannot be applied.
UPDATE jobs             SET status = 'failed' WHERE status = 'interrupted';
UPDATE job_destinations SET status = 'failed' WHERE status = 'interrupted';

ALTER TABLE jobs DROP CONSTRAINT jobs_status_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled'));

ALTER TABLE job_destinations DROP CONSTRAINT job_destinations_status_check;
ALTER TABLE job_destinations ADD CONSTRAINT job_destinations_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled'));
