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
-- removed with it.
ALTER TABLE notification_delivery_queue ADD COLUMN provider_id TEXT
    REFERENCES notification_providers (id) ON DELETE CASCADE;
ALTER TABLE notification_delivery_queue DROP CONSTRAINT ndq_type_check;
ALTER TABLE notification_delivery_queue ADD CONSTRAINT ndq_type_check
    CHECK (type IN ('email', 'webhook', 'provider'));
CREATE INDEX IF NOT EXISTS idx_ndq_provider_id ON notification_delivery_queue (provider_id);
