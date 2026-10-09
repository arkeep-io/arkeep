-- The GUI saves an S3 destination's folder inside the bucket as config.prefix,
-- but the server used to read only config.path, so every S3 repository was
-- created at the bucket root whatever prefix was entered. The server now
-- honours prefix: drop it from existing S3 destinations so they keep pointing
-- at the bucket root, where their snapshots actually are. A destination whose
-- repository should live in a folder must be recreated with the prefix.
-- Rows whose config is not valid JSON are left alone (row by row, so this
-- does not depend on pg_input_is_valid, which needs PostgreSQL 16).
DO $$
DECLARE
    r RECORD;
BEGIN
    FOR r IN SELECT id, config FROM destinations WHERE type = 's3' AND config LIKE '%"prefix"%' LOOP
        BEGIN
            UPDATE destinations SET config = (r.config::jsonb - 'prefix')::text WHERE id = r.id;
        EXCEPTION WHEN invalid_text_representation THEN
            NULL;
        END;
    END LOOP;
END $$;
