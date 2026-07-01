ALTER TABLE destinations ADD COLUMN repo_size_bytes BIGINT NOT NULL DEFAULT 0;
ALTER TABLE destinations ADD COLUMN repo_size_updated_at TIMESTAMP;
