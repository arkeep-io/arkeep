-- Migration: 000039_password_change_required
-- Lets an admin set a user's password and require a new one at next sign-in.
--
-- users.must_change_password makes a local login stop after the password (and
-- the second factor, when enabled) and ask for a new password before any
-- session is issued.
--
-- password_change_challenges holds that interim step, like
-- two_factor_challenges does for the code: only the SHA-256 hash of the token
-- is stored, it is short-lived and single-use. It is a table of its own so a
-- token issued for one step can never complete the other.

ALTER TABLE users ADD COLUMN must_change_password BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS password_change_challenges (
    id          TEXT        NOT NULL PRIMARY KEY,
    user_id     TEXT        NOT NULL,
    token_hash  TEXT        NOT NULL,
    expires_at  TIMESTAMP   NOT NULL,
    used_at     TIMESTAMP,
    created_at  TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT fk_pcc_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_pcc_token_hash ON password_change_challenges (token_hash);
CREATE INDEX IF NOT EXISTS idx_pcc_user_id    ON password_change_challenges (user_id);
