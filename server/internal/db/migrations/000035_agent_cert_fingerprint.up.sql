-- Binds each agent to the mTLS client certificate it registers with (SEC-24):
-- every RPC used to trust the agent_id written in the message, so any enrolled
-- agent could act as any other. Set on the agent's first registration after
-- this upgrade (trust on first use) and checked on every RPC afterwards.
-- NULL means not bound yet: the next certificate that registers as the agent
-- binds it, which is also how an admin re-binds an agent (reset-identity).
ALTER TABLE agents ADD COLUMN cert_fingerprint TEXT;

-- One certificate identifies one live agent. A soft-deleted agent keeps its
-- fingerprint but no longer holds the certificate, so the same machine can
-- register again as a new agent.
CREATE UNIQUE INDEX uq_agents_cert_fingerprint ON agents (cert_fingerprint)
    WHERE cert_fingerprint IS NOT NULL AND deleted_at IS NULL;
