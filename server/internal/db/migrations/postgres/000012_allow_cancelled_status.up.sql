ALTER TABLE jobs DROP CONSTRAINT jobs_status_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled'));

ALTER TABLE job_destinations DROP CONSTRAINT job_destinations_status_check;
ALTER TABLE job_destinations ADD CONSTRAINT job_destinations_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled'));

CREATE UNIQUE INDEX IF NOT EXISTS uq_policy_destinations_policy_dest ON policy_destinations (policy_id, destination_id);
