-- Migration: 000001_initial_schema
-- Creates the full initial database schema for Arkeep.
--
-- Foreign key constraints are defined explicitly to ensure referential integrity
-- across all drivers. SQLite enforces FK constraints only when
-- PRAGMA foreign_keys = ON is set — the GORM SQLite driver handles this
-- automatically via ConnectHook.
--
-- All primary keys are UUID v7 (text) generated at the application layer.
-- Indexes on foreign keys and frequently filtered columns are created explicitly
-- since GORM does not create them automatically from struct tags in migrations.

-- -----------------------------------------------------------------------------
-- Users & Auth
-- -----------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS users (
    id              TEXT        NOT NULL PRIMARY KEY,
    created_at      DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    email           TEXT        NOT NULL,
    password        TEXT        NOT NULL DEFAULT '',
    display_name    TEXT        NOT NULL,
    role            TEXT        NOT NULL DEFAULT 'user',
    is_active       INTEGER     NOT NULL DEFAULT 1,
    oidc_provider   TEXT        NOT NULL DEFAULT '',
    oidc_sub        TEXT        NOT NULL DEFAULT '',
    last_login_at   DATETIME,

    CONSTRAINT users_role_check CHECK (role IN ('admin', 'user'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users (email);

-- -----------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id          TEXT        NOT NULL PRIMARY KEY,
    created_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    user_id     TEXT        NOT NULL,
    token_hash  TEXT        NOT NULL,
    expires_at  DATETIME    NOT NULL,
    revoked_at  DATETIME,
    user_agent  TEXT        NOT NULL DEFAULT '',
    ip_address  TEXT        NOT NULL DEFAULT '',

    CONSTRAINT fk_refresh_tokens_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id     ON refresh_tokens (user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_refresh_tokens_hash ON refresh_tokens (token_hash);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires_at  ON refresh_tokens (expires_at);

-- -----------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS oidc_providers (
    id              TEXT        NOT NULL PRIMARY KEY,
    created_at      DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    name            TEXT        NOT NULL,
    issuer          TEXT        NOT NULL,
    client_id       TEXT        NOT NULL,
    client_secret   TEXT        NOT NULL,
    redirect_url    TEXT        NOT NULL,
    scopes          TEXT        NOT NULL DEFAULT 'openid email profile',
    enabled         INTEGER     NOT NULL DEFAULT 0
);

-- -----------------------------------------------------------------------------
-- Agents
-- -----------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS agents (
    id                  TEXT        NOT NULL PRIMARY KEY,
    created_at          DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at          DATETIME,
    name                TEXT        NOT NULL,
    hostname            TEXT        NOT NULL,
    ip_address          TEXT        NOT NULL DEFAULT '',
    version             TEXT        NOT NULL DEFAULT '',
    status              TEXT        NOT NULL DEFAULT 'offline',
    last_seen_at        DATETIME,
    registration_token  TEXT        NOT NULL DEFAULT '',
    labels              TEXT        NOT NULL DEFAULT '{}',

    CONSTRAINT agents_status_check CHECK (status IN ('online', 'offline', 'error'))
);

CREATE INDEX IF NOT EXISTS idx_agents_deleted_at ON agents (deleted_at);

-- -----------------------------------------------------------------------------
-- Destinations
-- -----------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS destinations (
    id          TEXT        NOT NULL PRIMARY KEY,
    created_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    name        TEXT        NOT NULL,
    type        TEXT        NOT NULL,
    credentials TEXT        NOT NULL DEFAULT '',
    config      TEXT        NOT NULL DEFAULT '{}',
    enabled     INTEGER     NOT NULL DEFAULT 1,

    CONSTRAINT destinations_type_check CHECK (type IN ('local', 's3', 'sftp', 'rest', 'rclone'))
);

-- -----------------------------------------------------------------------------
-- Policies
-- -----------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS policies (
    id                  TEXT        NOT NULL PRIMARY KEY,
    created_at          DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at          DATETIME,
    name                TEXT        NOT NULL,
    agent_id            TEXT        NOT NULL,
    schedule            TEXT        NOT NULL,
    enabled             INTEGER     NOT NULL DEFAULT 1,
    sources             TEXT        NOT NULL DEFAULT '[]',
    retention_daily     INTEGER     NOT NULL DEFAULT 7,
    retention_weekly    INTEGER     NOT NULL DEFAULT 4,
    retention_monthly   INTEGER     NOT NULL DEFAULT 6,
    retention_yearly    INTEGER     NOT NULL DEFAULT 1,
    repo_password       TEXT        NOT NULL DEFAULT '',
    hook_pre_backup     TEXT        NOT NULL DEFAULT '',
    hook_post_backup    TEXT        NOT NULL DEFAULT '',
    last_run_at         DATETIME,
    next_run_at         DATETIME,

    CONSTRAINT fk_policies_agent FOREIGN KEY (agent_id) REFERENCES agents (id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_policies_agent_id   ON policies (agent_id);
CREATE INDEX IF NOT EXISTS idx_policies_deleted_at ON policies (deleted_at);
CREATE INDEX IF NOT EXISTS idx_policies_enabled    ON policies (enabled);

-- -----------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS policy_destinations (
    id              TEXT        NOT NULL PRIMARY KEY,
    created_at      DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    policy_id       TEXT        NOT NULL,
    destination_id  TEXT        NOT NULL,
    priority        INTEGER     NOT NULL DEFAULT 0,

    CONSTRAINT fk_policy_destinations_policy      FOREIGN KEY (policy_id)      REFERENCES policies     (id) ON DELETE CASCADE,
    CONSTRAINT fk_policy_destinations_destination FOREIGN KEY (destination_id) REFERENCES destinations (id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_policy_destinations_policy_id      ON policy_destinations (policy_id);
CREATE INDEX IF NOT EXISTS idx_policy_destinations_destination_id ON policy_destinations (destination_id);

-- -----------------------------------------------------------------------------
-- Jobs
-- -----------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS jobs (
    id          TEXT        NOT NULL PRIMARY KEY,
    created_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    policy_id   TEXT        NOT NULL,
    agent_id    TEXT        NOT NULL,
    status      TEXT        NOT NULL DEFAULT 'pending',
    started_at  DATETIME,
    ended_at    DATETIME,
    error       TEXT        NOT NULL DEFAULT '',

    CONSTRAINT fk_jobs_policy FOREIGN KEY (policy_id) REFERENCES policies (id) ON DELETE RESTRICT,
    CONSTRAINT fk_jobs_agent  FOREIGN KEY (agent_id)  REFERENCES agents  (id) ON DELETE RESTRICT,
    CONSTRAINT jobs_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_jobs_policy_id ON jobs (policy_id);
CREATE INDEX IF NOT EXISTS idx_jobs_agent_id  ON jobs (agent_id);
CREATE INDEX IF NOT EXISTS idx_jobs_status    ON jobs (status);

-- -----------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS job_destinations (
    id              TEXT        NOT NULL PRIMARY KEY,
    created_at      DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    job_id          TEXT        NOT NULL,
    destination_id  TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'pending',
    snapshot_id     TEXT        NOT NULL DEFAULT '',
    size_bytes      INTEGER     NOT NULL DEFAULT 0,
    started_at      DATETIME,
    ended_at        DATETIME,
    error           TEXT        NOT NULL DEFAULT '',

    CONSTRAINT fk_job_destinations_job         FOREIGN KEY (job_id)         REFERENCES jobs         (id) ON DELETE CASCADE,
    CONSTRAINT fk_job_destinations_destination FOREIGN KEY (destination_id) REFERENCES destinations (id) ON DELETE RESTRICT,
    CONSTRAINT job_destinations_status_check   CHECK (status IN ('pending', 'running', 'succeeded', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_job_destinations_job_id         ON job_destinations (job_id);
CREATE INDEX IF NOT EXISTS idx_job_destinations_destination_id ON job_destinations (destination_id);

-- -----------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS job_logs (
    id          TEXT        NOT NULL PRIMARY KEY,
    created_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    job_id      TEXT        NOT NULL,
    level       TEXT        NOT NULL,
    message     TEXT        NOT NULL,
    timestamp   DATETIME    NOT NULL,

    CONSTRAINT fk_job_logs_job   FOREIGN KEY (job_id) REFERENCES jobs (id) ON DELETE CASCADE,
    CONSTRAINT job_logs_level_check CHECK (level IN ('info', 'warn', 'error'))
);

CREATE INDEX IF NOT EXISTS idx_job_logs_job_id    ON job_logs (job_id);
CREATE INDEX IF NOT EXISTS idx_job_logs_timestamp ON job_logs (timestamp);

-- -----------------------------------------------------------------------------
-- Snapshots
-- -----------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS snapshots (
    id              TEXT        NOT NULL PRIMARY KEY,
    created_at      DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    policy_id       TEXT        NOT NULL,
    destination_id  TEXT        NOT NULL,
    job_id          TEXT        NOT NULL,
    snapshot_id     TEXT        NOT NULL,
    size_bytes      INTEGER     NOT NULL DEFAULT 0,
    file_count      INTEGER     NOT NULL DEFAULT 0,
    tags            TEXT        NOT NULL DEFAULT '[]',
    snapshot_at     DATETIME    NOT NULL,

    CONSTRAINT fk_snapshots_policy      FOREIGN KEY (policy_id)      REFERENCES policies     (id) ON DELETE RESTRICT,
    CONSTRAINT fk_snapshots_destination FOREIGN KEY (destination_id) REFERENCES destinations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_snapshots_job         FOREIGN KEY (job_id)         REFERENCES jobs         (id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_snapshots_policy_id      ON snapshots (policy_id);
CREATE INDEX IF NOT EXISTS idx_snapshots_destination_id ON snapshots (destination_id);
CREATE INDEX IF NOT EXISTS idx_snapshots_job_id         ON snapshots (job_id);
CREATE INDEX IF NOT EXISTS idx_snapshots_snapshot_id    ON snapshots (snapshot_id);
CREATE INDEX IF NOT EXISTS idx_snapshots_snapshot_at    ON snapshots (snapshot_at);

-- -----------------------------------------------------------------------------
-- Notifications
-- -----------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS notifications (
    id          TEXT        NOT NULL PRIMARY KEY,
    created_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    user_id     TEXT        NOT NULL,
    type        TEXT        NOT NULL,
    title       TEXT        NOT NULL,
    body        TEXT        NOT NULL,
    read_at     DATETIME,
    payload     TEXT        NOT NULL DEFAULT '{}',

    CONSTRAINT fk_notifications_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON notifications (user_id);
CREATE INDEX IF NOT EXISTS idx_notifications_read_at ON notifications (read_at);