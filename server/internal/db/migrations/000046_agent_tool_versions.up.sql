-- Migration: 000046_agent_tool_versions
-- The versions of the restic and rclone binaries embedded in each agent
-- (issue #508), reported on every registration. Empty until the agent next
-- registers, and for agents older than the report.
ALTER TABLE agents ADD COLUMN restic_version TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN rclone_version TEXT NOT NULL DEFAULT '';
