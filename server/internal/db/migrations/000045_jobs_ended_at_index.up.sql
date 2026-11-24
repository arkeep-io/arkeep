-- Migration: 000045_jobs_ended_at_index
-- The job report email selects the jobs that ended since the previous report
-- (JobRepository.ListEndedBetween).
CREATE INDEX IF NOT EXISTS idx_jobs_ended_at ON jobs (ended_at);
