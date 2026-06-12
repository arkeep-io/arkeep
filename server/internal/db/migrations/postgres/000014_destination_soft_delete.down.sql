DROP INDEX IF EXISTS idx_destinations_deleted_at;
ALTER TABLE destinations DROP COLUMN IF EXISTS deleted_at;
