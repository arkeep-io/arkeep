-- Issue #267: 000024_destination_retention declared these flags as INTEGER in
-- a shared migration. SQLite doesn't mind, but on PostgreSQL pgx refuses to
-- encode the Go bool from db.Destination into int4, which made the retention
-- scheduler (and therefore server startup) fail on v0.6.0.
ALTER TABLE destinations ALTER COLUMN retention_enabled DROP DEFAULT;
ALTER TABLE destinations ALTER COLUMN retention_enabled TYPE BOOLEAN USING retention_enabled <> 0;
ALTER TABLE destinations ALTER COLUMN retention_enabled SET DEFAULT FALSE;

ALTER TABLE destinations ALTER COLUMN append_only DROP DEFAULT;
ALTER TABLE destinations ALTER COLUMN append_only TYPE BOOLEAN USING append_only <> 0;
ALTER TABLE destinations ALTER COLUMN append_only SET DEFAULT FALSE;

ALTER TABLE destinations ALTER COLUMN retention_needs_review DROP DEFAULT;
ALTER TABLE destinations ALTER COLUMN retention_needs_review TYPE BOOLEAN USING retention_needs_review <> 0;
ALTER TABLE destinations ALTER COLUMN retention_needs_review SET DEFAULT FALSE;

-- On v0.6.0 the one-time retention backfill (server/cmd/server/
-- retention_backfill.go) hit the same encode error for every destination but
-- still recorded itself as done. Clear the marker so it runs again now that
-- the columns are writable. Safe: the server never finished starting on
-- PostgreSQL with v0.6.0, so no admin can have edited retention since, and
-- the legacy policies.retention_* columns it reads from are still present.
DELETE FROM settings WHERE key = 'migration.destination_retention_backfilled';
