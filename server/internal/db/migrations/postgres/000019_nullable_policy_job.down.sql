-- Restores NOT NULL on snapshots.policy_id / snapshots.job_id / jobs.policy_id.
--
-- DESTRUCTIVE: rows that legitimately carry NULL cannot satisfy the restored
-- constraint, so they are deleted — imported snapshots and any restore job
-- started from one. Review before running this against a real database.
-- job_destinations and job_logs are removed by ON DELETE CASCADE.
DELETE FROM snapshots WHERE policy_id IS NULL OR job_id IS NULL;
DELETE FROM jobs WHERE policy_id IS NULL;

ALTER TABLE snapshots ALTER COLUMN policy_id SET NOT NULL;
ALTER TABLE snapshots ALTER COLUMN job_id    SET NOT NULL;
ALTER TABLE jobs      ALTER COLUMN policy_id SET NOT NULL;
