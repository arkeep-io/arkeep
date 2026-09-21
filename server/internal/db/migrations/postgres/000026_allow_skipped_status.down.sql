UPDATE job_destinations SET status = 'failed' WHERE status = 'skipped';
UPDATE job_destination_commands SET status = 'failed' WHERE status = 'skipped';

ALTER TABLE job_destinations DROP CONSTRAINT job_destinations_status_check;
ALTER TABLE job_destinations ADD CONSTRAINT job_destinations_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted'));

ALTER TABLE job_destination_commands DROP CONSTRAINT job_destination_commands_status_check;
ALTER TABLE job_destination_commands ADD CONSTRAINT job_destination_commands_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted'));
