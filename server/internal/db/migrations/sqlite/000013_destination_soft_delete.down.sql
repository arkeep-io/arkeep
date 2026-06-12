DROP INDEX IF EXISTS idx_destinations_deleted_at;
ALTER TABLE destinations DROP COLUMN deleted_at;
