-- The GUI saves an S3 destination's folder inside the bucket as config.prefix,
-- but the server used to read only config.path, so every S3 repository was
-- created at the bucket root whatever prefix was entered. The server now
-- honours prefix: drop it from existing S3 destinations so they keep pointing
-- at the bucket root, where their snapshots actually are. A destination whose
-- repository should live in a folder must be recreated with the prefix.
-- Rows whose config is not valid JSON are left alone.
UPDATE destinations SET config = json_remove(config, '$.prefix')
WHERE type = 's3'
  AND CASE WHEN json_valid(config) THEN json_type(config, '$.prefix') IS NOT NULL ELSE 0 END;
