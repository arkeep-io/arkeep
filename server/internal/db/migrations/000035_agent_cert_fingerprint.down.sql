DROP INDEX IF EXISTS uq_agents_cert_fingerprint;
ALTER TABLE agents DROP COLUMN cert_fingerprint;
