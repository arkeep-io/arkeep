-- Migration: 000002_agent_docker_available (down)
-- SQLite does not support DROP COLUMN before version 3.35.0.
-- For PostgreSQL this would be: ALTER TABLE agents DROP COLUMN docker_available;
-- For SQLite we recreate the table without the column.

CREATE TABLE agents_backup AS SELECT
    id, created_at, updated_at, deleted_at,
    name, hostname, ip_address, os, arch, version,
    status, last_seen_at, labels
FROM agents;

DROP TABLE agents;

ALTER TABLE agents_backup RENAME TO agents;

CREATE INDEX IF NOT EXISTS idx_agents_deleted_at ON agents (deleted_at);