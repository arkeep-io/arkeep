-- Migration: 000018_two_factor
-- Adds TOTP-based two-factor authentication for local accounts.
--
-- users.totp_secret is typed EncryptedString in Go (AES-256-GCM at rest, same
-- as users.password and oidc_providers.client_secret). An empty string means
-- "no secret"; a non-empty secret with two_factor_enabled = false means
-- enrollment was started but never confirmed.
--
-- two_factor_challenges holds the short-lived interim state between a
-- successful password check and a successful TOTP check. Only the SHA-256 hash
-- of the challenge token is stored. The attempts column is the per-account
-- lockout: Arkeep has no other account-level throttle, and a per-IP rate limit
-- alone is weak against a six-digit code.
--
-- recovery_codes are single-use (used_at) and stored hashed. Rows in both
-- tables are removed automatically when the parent user is deleted.

ALTER TABLE users ADD COLUMN totp_secret        TEXT    NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN two_factor_enabled BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS two_factor_challenges (
    id          TEXT        NOT NULL PRIMARY KEY,
    user_id     TEXT        NOT NULL,
    token_hash  TEXT        NOT NULL,
    attempts    INTEGER     NOT NULL DEFAULT 0,
    expires_at  TIMESTAMP   NOT NULL,
    used_at     TIMESTAMP,
    created_at  TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT fk_tfc_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_tfc_token_hash ON two_factor_challenges (token_hash);
CREATE INDEX IF NOT EXISTS idx_tfc_user_id    ON two_factor_challenges (user_id);

CREATE TABLE IF NOT EXISTS recovery_codes (
    id          TEXT        NOT NULL PRIMARY KEY,
    user_id     TEXT        NOT NULL,
    code_hash   TEXT        NOT NULL,
    used_at     TIMESTAMP,
    created_at  TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT fk_rc_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_rc_code_hash ON recovery_codes (code_hash);
CREATE INDEX IF NOT EXISTS idx_rc_user_id   ON recovery_codes (user_id);
