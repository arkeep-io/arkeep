DROP INDEX IF EXISTS idx_ndq_provider_id;
DELETE FROM notification_delivery_queue WHERE type = 'provider';
ALTER TABLE notification_delivery_queue DROP CONSTRAINT ndq_type_check;
ALTER TABLE notification_delivery_queue ADD CONSTRAINT ndq_type_check
    CHECK (type IN ('email', 'webhook'));
ALTER TABLE notification_delivery_queue DROP COLUMN provider_id;
DROP TABLE IF EXISTS notification_providers;
