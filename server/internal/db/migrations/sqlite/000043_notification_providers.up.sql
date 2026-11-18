-- Notification providers (issue #446): push and chat services such as Gotify,
-- ntfy, Telegram or Discord, delivered through shoutrrr URLs built by the
-- server from the provider's fields (ADR 0019). This migration is per driver
-- because SQLite must rebuild notification_delivery_queue to change its CHECK.
--
-- config holds the non-secret fields as JSON; secrets holds the secret ones as
-- JSON encrypted at rest with ARKEEP_SECRET_KEY. type is validated by the
-- server, not by a CHECK, so a new service needs no table rebuild.
-- priority_* is shoutrrr's priority parameter for that event ('' = default).
CREATE TABLE IF NOT EXISTS notification_providers (
    id                     TEXT      NOT NULL PRIMARY KEY,
    created_at             TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at             TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    name                   TEXT      NOT NULL,
    type                   TEXT      NOT NULL,
    config                 TEXT      NOT NULL DEFAULT '{}',
    secrets                TEXT      NOT NULL DEFAULT '',
    enabled                BOOLEAN   NOT NULL DEFAULT true,
    priority_job_success   TEXT      NOT NULL DEFAULT '',
    priority_job_failure   TEXT      NOT NULL DEFAULT '',
    priority_agent_offline TEXT      NOT NULL DEFAULT '',
    priority_agent_online  TEXT      NOT NULL DEFAULT ''
);

-- A delivery to a provider has type 'provider' and names the provider; it is
-- removed with it. SQLite cannot alter a CHECK constraint, so the queue is
-- recreated. No table references it.
CREATE TABLE notification_delivery_queue_new (
    id              TEXT      NOT NULL PRIMARY KEY,
    notification_id TEXT      NOT NULL,
    type            TEXT      NOT NULL,
    status          TEXT      NOT NULL DEFAULT 'pending',
    attempts        INTEGER   NOT NULL DEFAULT 0,
    last_error      TEXT      NOT NULL DEFAULT '',
    next_retry_at   TIMESTAMP,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    provider_id     TEXT,

    CONSTRAINT fk_ndq_notification FOREIGN KEY (notification_id) REFERENCES notifications (id) ON DELETE CASCADE,
    CONSTRAINT fk_ndq_provider     FOREIGN KEY (provider_id) REFERENCES notification_providers (id) ON DELETE CASCADE,
    CONSTRAINT ndq_type_check   CHECK (type   IN ('email', 'webhook', 'provider')),
    CONSTRAINT ndq_status_check CHECK (status IN ('pending', 'sent', 'exhausted'))
);
INSERT INTO notification_delivery_queue_new
    (id, notification_id, type, status, attempts, last_error, next_retry_at, created_at, updated_at)
    SELECT id, notification_id, type, status, attempts, last_error, next_retry_at, created_at, updated_at
    FROM notification_delivery_queue;
DROP TABLE notification_delivery_queue;
ALTER TABLE notification_delivery_queue_new RENAME TO notification_delivery_queue;
CREATE INDEX IF NOT EXISTS idx_ndq_notification_id ON notification_delivery_queue (notification_id);
CREATE INDEX IF NOT EXISTS idx_ndq_status_retry    ON notification_delivery_queue (status, next_retry_at);
CREATE INDEX IF NOT EXISTS idx_ndq_provider_id     ON notification_delivery_queue (provider_id);
