-- Reverts the 'waiting' status. Queued jobs never ran, so they are folded into
-- 'failed' first, otherwise the narrower CHECK constraint cannot be applied.
UPDATE jobs SET status = 'failed', error = 'queue removed by migration rollback' WHERE status = 'waiting';

ALTER TABLE jobs DROP CONSTRAINT jobs_status_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted'));
