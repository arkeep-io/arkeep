-- Migration: 000042_jobs_policy_created_index
-- The policies list ranks each policy's backups by created_at to show its
-- recent runs, and the policy jobs page lists them in the same order. Jobs are
-- never pruned, so without this index both sort a policy's whole history.
--
-- A SQLite migration that rebuilds the jobs table must recreate this index
-- along with idx_jobs_policy_id, idx_jobs_agent_id and idx_jobs_status.
CREATE INDEX IF NOT EXISTS idx_jobs_policy_id_created_at ON jobs (policy_id, created_at);
