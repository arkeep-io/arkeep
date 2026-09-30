-- Per-policy override of the global job_success / job_failure notification
-- toggles (email + webhook). 'inherit' follows the global setting, 'always'
-- and 'never' replace it for this policy's jobs.
ALTER TABLE policies ADD COLUMN notify_on_success TEXT NOT NULL DEFAULT 'inherit';
ALTER TABLE policies ADD COLUMN notify_on_failure TEXT NOT NULL DEFAULT 'inherit';
