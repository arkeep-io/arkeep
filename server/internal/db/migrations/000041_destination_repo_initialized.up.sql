-- Migration: 000041_destination_repo_initialized
-- destinations.repo_initialized_at records that the destination's restic
-- repository is known to exist: set the first time anything succeeds against
-- it. From then on the agent no longer runs `restic init` before a backup, so
-- a repository that has gone missing (an unmounted disk, an emptied bucket)
-- fails the job instead of being silently replaced by a new, empty one.
--
-- Existing destinations with a successful job, a snapshot record or a measured
-- repository size already have a repository.
ALTER TABLE destinations ADD COLUMN repo_initialized_at TIMESTAMP;

UPDATE destinations SET repo_initialized_at = CURRENT_TIMESTAMP
WHERE repo_size_updated_at IS NOT NULL
   OR EXISTS (SELECT 1 FROM job_destinations jd
              WHERE jd.destination_id = destinations.id AND jd.status = 'succeeded')
   OR EXISTS (SELECT 1 FROM job_destination_commands jc
              WHERE jc.destination_id = destinations.id AND jc.status = 'succeeded')
   OR EXISTS (SELECT 1 FROM snapshots s WHERE s.destination_id = destinations.id);
