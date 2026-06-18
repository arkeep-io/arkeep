-- Migration: 000016_password_reset_tokens
-- Adds a password_reset_tokens table backing the self-service "forgot password"
-- flow for local (non-OIDC) accounts. Only the SHA-256 hash of the reset token
-- is stored — the raw token lives only in the email link sent to the user.
--
-- Tokens are single-use (marked via used_at) and short-lived (expires_at). A new
-- request for the same user invalidates any previous tokens (DELETE by user_id).
-- Rows are removed automatically when the parent user is deleted (ON DELETE CASCADE).

CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id          TEXT        NOT NULL PRIMARY KEY,
    user_id     TEXT        NOT NULL,
    token_hash  TEXT        NOT NULL,
    expires_at  TIMESTAMP   NOT NULL,
    used_at     TIMESTAMP,
    created_at  TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT fk_prt_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_prt_token_hash ON password_reset_tokens (token_hash);
CREATE INDEX IF NOT EXISTS idx_prt_user_id    ON password_reset_tokens (user_id);
