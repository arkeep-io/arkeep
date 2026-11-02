-- Migration: 000040_drop_agent_labels
-- agents.labels was writable through the API but read by nothing (no GUI,
-- no filter, no policy targeting), and never validated as JSON. Removed with
-- the API fields and the placeholder POST /agents.
ALTER TABLE agents DROP COLUMN labels;
