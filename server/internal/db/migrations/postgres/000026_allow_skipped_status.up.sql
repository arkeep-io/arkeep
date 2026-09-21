-- Adds the 'skipped' status: a destination excluded from a job's run because
-- the server's busy-gate found another operation (backup or retention)
-- already in progress against that same repository (see destinations.
-- busy_job_id, issue #130). Kept distinct from 'failed' so a routine,
-- expected deferral doesn't read as an error.
ALTER TABLE job_destinations DROP CONSTRAINT job_destinations_status_check;
ALTER TABLE job_destinations ADD CONSTRAINT job_destinations_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted', 'skipped'));

ALTER TABLE job_destination_commands DROP CONSTRAINT job_destination_commands_status_check;
ALTER TABLE job_destination_commands ADD CONSTRAINT job_destination_commands_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted', 'skipped'));
