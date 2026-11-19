CREATE TABLE notification_delivery_queue_old (
    id              TEXT      NOT NULL PRIMARY KEY,
    notification_id TEXT      NOT NULL,
    type            TEXT      NOT NULL,
    status          TEXT      NOT NULL DEFAULT 'pending',
    attempts        INTEGER   NOT NULL DEFAULT 0,
    last_error      TEXT      NOT NULL DEFAULT '',
    next_retry_at   TIMESTAMP,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT fk_ndq_notification FOREIGN KEY (notification_id) REFERENCES notifications (id) ON DELETE CASCADE,
    CONSTRAINT ndq_type_check   CHECK (type   IN ('email', 'webhook')),
    CONSTRAINT ndq_status_check CHECK (status IN ('pending', 'sent', 'exhausted'))
);
INSERT INTO notification_delivery_queue_old
    SELECT id, notification_id, type, status, attempts, last_error, next_retry_at, created_at, updated_at
    FROM notification_delivery_queue WHERE type <> 'provider';
DROP TABLE notification_delivery_queue;
ALTER TABLE notification_delivery_queue_old RENAME TO notification_delivery_queue;
CREATE INDEX IF NOT EXISTS idx_ndq_notification_id ON notification_delivery_queue (notification_id);
CREATE INDEX IF NOT EXISTS idx_ndq_status_retry    ON notification_delivery_queue (status, next_retry_at);
DROP TABLE IF EXISTS notification_providers;
