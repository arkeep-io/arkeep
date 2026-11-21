-- Migration: 000045_repository_repair
-- Repository repairs run from the GUI under a destination maintenance mode
-- (ADR 0026).
--
-- jobs.params holds the parameters of a job started from the GUI with
-- options, as JSON without secrets (for a repair: its action and filters), and
-- jobs.requested_by the email of the admin who started it. Both stay '' for
-- jobs started by a schedule.
ALTER TABLE jobs ADD COLUMN params TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN requested_by TEXT NOT NULL DEFAULT '';

-- A destination is in maintenance while maintenance_since is set: no job can
-- claim it, so scheduled work waits in the queue. maintenance_job_id is set
-- when a repair put the destination in maintenance for its own duration: the
-- maintenance then lasts only while that job is pending or running. No FK on
-- the job id, as for last_check_job_id: job-log retention may delete the job.
ALTER TABLE destinations ADD COLUMN maintenance_since TIMESTAMP;
ALTER TABLE destinations ADD COLUMN maintenance_by TEXT NOT NULL DEFAULT '';
ALTER TABLE destinations ADD COLUMN maintenance_job_id TEXT;

-- Whether the agent reported AgentCapabilities.repair at its last
-- registration: its restic has the repair commands.
ALTER TABLE agents ADD COLUMN repair_available BOOLEAN NOT NULL DEFAULT false;
