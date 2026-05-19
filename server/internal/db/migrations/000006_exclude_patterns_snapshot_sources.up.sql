ALTER TABLE policies  ADD COLUMN exclude_patterns TEXT NOT NULL DEFAULT '[]';
ALTER TABLE snapshots ADD COLUMN sources          TEXT NOT NULL DEFAULT '[]';
