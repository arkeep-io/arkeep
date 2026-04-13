CREATE TABLE IF NOT EXISTS audit_log (
  id            TEXT      NOT NULL PRIMARY KEY,
  user_id       TEXT      NOT NULL,
  user_email    TEXT      NOT NULL,
  action        TEXT      NOT NULL,
  resource_type TEXT      NOT NULL DEFAULT '',
  resource_id   TEXT      NOT NULL DEFAULT '',
  details       TEXT      NOT NULL DEFAULT '{}',
  ip_address    TEXT      NOT NULL DEFAULT '',
  created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_audit_log_user_id    ON audit_log (user_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_action     ON audit_log (action);
CREATE INDEX IF NOT EXISTS idx_audit_log_created_at ON audit_log (created_at);
