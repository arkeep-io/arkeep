-- Migration: 000048_api_tokens
-- Read-only API tokens (ADR 0027): long-lived credentials a user creates for a
-- program that reads the API on their behalf, such as a Home Assistant
-- integration. Only the SHA-256 hash of a token is stored; hint keeps its first
-- characters so a user can tell their tokens apart. scope is 'read', the only
-- scope so far. expires_at NULL means no expiry. Revoking a token deletes its
-- row, and deleting a user deletes their tokens (ON DELETE CASCADE).

CREATE TABLE IF NOT EXISTS api_tokens (
    id            TEXT        NOT NULL PRIMARY KEY,
    user_id       TEXT        NOT NULL,
    name          TEXT        NOT NULL,
    token_hash    TEXT        NOT NULL,
    hint          TEXT        NOT NULL,
    scope         TEXT        NOT NULL DEFAULT 'read',
    expires_at    TIMESTAMP,
    last_used_at  TIMESTAMP,
    created_at    TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT fk_api_tokens_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_api_tokens_token_hash ON api_tokens (token_hash);
CREATE INDEX IF NOT EXISTS idx_api_tokens_user_id ON api_tokens (user_id);
