ALTER TABLE destinations ADD COLUMN deleted_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_destinations_deleted_at ON destinations(deleted_at);
