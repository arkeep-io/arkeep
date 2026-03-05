-- Migration: 000002_agent_docker_available
-- Adds docker_available to the agents table to persist whether the agent
-- can reach the Docker daemon on its host. This is advertised by the agent
-- in the Register RPC via AgentCapabilities.docker and stored here so the
-- GUI can show or hide the Docker volume source option without requiring
-- the agent to be currently connected.

ALTER TABLE agents ADD COLUMN docker_available INTEGER NOT NULL DEFAULT 0;