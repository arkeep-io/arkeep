-- Automatic resume of backups interrupted by an agent disconnection.
--
-- resume_interrupted opts a policy in or out. Default TRUE: a laptop losing
-- power mid-backup is the case this exists for, and resuming is cheap because
-- restic keeps the packs already uploaded and only transfers what is missing.
ALTER TABLE policies ADD COLUMN resume_interrupted BOOLEAN NOT NULL DEFAULT TRUE;

-- resume_of_job_id is the audit chain: which interrupted job this run resumes.
-- Deliberately without a foreign key, since the original job can be removed by
-- job retention while this row survives.
ALTER TABLE jobs ADD COLUMN resume_of_job_id TEXT;

-- resume_attempt counts consecutive resumes, so a machine that dies at the same
-- point every time stops being retried instead of looping forever.
ALTER TABLE jobs ADD COLUMN resume_attempt INTEGER NOT NULL DEFAULT 0;
