ALTER TABLE destinations ALTER COLUMN retention_needs_review DROP DEFAULT;
ALTER TABLE destinations ALTER COLUMN retention_needs_review TYPE INTEGER USING CASE WHEN retention_needs_review THEN 1 ELSE 0 END;
ALTER TABLE destinations ALTER COLUMN retention_needs_review SET DEFAULT 0;

ALTER TABLE destinations ALTER COLUMN append_only DROP DEFAULT;
ALTER TABLE destinations ALTER COLUMN append_only TYPE INTEGER USING CASE WHEN append_only THEN 1 ELSE 0 END;
ALTER TABLE destinations ALTER COLUMN append_only SET DEFAULT 0;

ALTER TABLE destinations ALTER COLUMN retention_enabled DROP DEFAULT;
ALTER TABLE destinations ALTER COLUMN retention_enabled TYPE INTEGER USING CASE WHEN retention_enabled THEN 1 ELSE 0 END;
ALTER TABLE destinations ALTER COLUMN retention_enabled SET DEFAULT 0;
