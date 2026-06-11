DELETE FROM job_destinations WHERE status = 'cancelled';
DELETE FROM jobs WHERE status = 'cancelled';

ALTER TABLE jobs DROP CONSTRAINT jobs_status_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed'));

ALTER TABLE job_destinations DROP CONSTRAINT job_destinations_status_check;
ALTER TABLE job_destinations ADD CONSTRAINT job_destinations_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed'));

DROP INDEX IF EXISTS uq_policy_destinations_policy_dest;
