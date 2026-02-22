-- Settings table: generic key-value store for server-side configuration.
-- Keys are namespaced by convention: "smtp.*", "webhook.*", etc.
-- Values are plain TEXT; sensitive values (e.g. smtp.password) are encrypted
-- at the application layer via EncryptedString before being written here.
CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY NOT NULL,
    value      TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);