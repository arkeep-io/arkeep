-- Migration: 000004_notification_delivery
-- Adds a notification_delivery_queue table for reliable delivery tracking of
-- email and webhook notifications. Each row represents a single delivery attempt
-- for a single channel (email or webhook) for a single notification.
--
-- Rationale for a separate table (rather than columns on notifications):
--   - Additive migration: no ALTER TABLE on existing data
--   - Clean separation of concerns: notification (event) vs delivery (channel state)
--   - Uniform schema for all future delivery channels (just a new `type` value)
--   - ON DELETE CASCADE: delivery rows are automatically removed with the notification
--
-- Status lifecycle:
--   pending   → delivery not yet attempted or scheduled for retry
--   sent      → delivery succeeded
--   exhausted → max retries exceeded; no further attempts will be made
--
-- The background retrier (notification.NotificationService.Start) polls every
-- 30 seconds for rows where status='pending' AND next_retry_at <= NOW().
-- Max 3 retry attempts with exponential backoff: +5min → +30min → exhausted.

CREATE TABLE IF NOT EXISTS notification_delivery_queue (
    id              TEXT        NOT NULL PRIMARY KEY,
    notification_id TEXT        NOT NULL,
    type            TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'pending',
    attempts        INTEGER     NOT NULL DEFAULT 0,
    last_error      TEXT        NOT NULL DEFAULT '',
    next_retry_at   TIMESTAMP,
    created_at      TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT fk_ndq_notification FOREIGN KEY (notification_id) REFERENCES notifications (id) ON DELETE CASCADE,
    CONSTRAINT ndq_type_check   CHECK (type   IN ('email', 'webhook')),
    CONSTRAINT ndq_status_check CHECK (status IN ('pending', 'sent', 'exhausted'))
);

CREATE INDEX IF NOT EXISTS idx_ndq_notification_id ON notification_delivery_queue (notification_id);
-- Composite index used by the retrier query: WHERE status='pending' AND next_retry_at <= ?
CREATE INDEX IF NOT EXISTS idx_ndq_status_retry    ON notification_delivery_queue (status, next_retry_at);
