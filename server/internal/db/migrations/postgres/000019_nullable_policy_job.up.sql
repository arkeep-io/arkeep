-- Snapshots imported from a pre-existing Restic repository belong to a
-- destination but to no policy and no job: they were not produced by a backup
-- run. The same applies to a restore job started from such a snapshot, which
-- has no policy either. The foreign keys are kept — in SQL a foreign key
-- accepts NULL, so referential integrity still holds for non-imported rows.
ALTER TABLE snapshots ALTER COLUMN policy_id DROP NOT NULL;
ALTER TABLE snapshots ALTER COLUMN job_id    DROP NOT NULL;
ALTER TABLE jobs      ALTER COLUMN policy_id DROP NOT NULL;
