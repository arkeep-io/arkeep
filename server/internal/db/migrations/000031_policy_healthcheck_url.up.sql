-- Optional Healthchecks.io (or compatible) ping URL per policy (issue #294).
-- Empty means no pings. Backup jobs ping /start, the URL itself on success
-- and /fail on failure or cancel.
ALTER TABLE policies ADD COLUMN healthcheck_url TEXT NOT NULL DEFAULT '';
