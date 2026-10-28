# 0003. Use restic and rclone, embedded in the agent

- **Status:** Accepted
- **Date:** 2026-10-12 (retro-documented; the decision predates this record)
- **Deciders:** Arkeep maintainers

## Context

A backup tool must deduplicate, encrypt, chunk and verify data reliably.
Writing a storage engine is a large, risky effort, and users want to keep
access to their backups even without Arkeep. Agents also run on very different
hosts (Linux, macOS, Windows, embedded ARM and MIPS devices), where installing
extra packages may be impossible.

## Decision

- Arkeep uses **restic** as its backup engine and **rclone** as the transport
  for backends restic does not support natively. Arkeep is a management layer
  on top: scheduling, coordination, GUI, notifications.
- Repositories are standard restic repositories that the `restic` CLI can
  read directly.
- The restic and rclone binaries for each platform are **embedded in the agent
  binary** at build time (`go:embed`) and extracted to the agent's state
  directory on first use. Each agent build embeds only its own platform's
  binaries.
- Only `agent/internal/restic` may invoke restic or rclone. It passes
  credentials through the subprocess environment, filtered by an allowlist.

## Consequences

- No vendor lock-in: backups remain usable with the restic CLI alone.
- The agent is a single file to deploy, with no runtime dependency to install.
- The agent binary is large: over 100 MB per platform, rclone alone
  accounting for 75–90 MB.
- Arkeep's security depends on the integrity of the embedded upstream
  binaries, which run with the agent's privileges. Their versions and
  integrity must be controlled at build time.
- Upgrading restic or rclone requires a new agent release.

## Alternatives considered

- **Build our own engine** — far more work and risk, and backups would only be
  readable with Arkeep.
- **Require restic/rclone to be installed on each host** — version drift across
  hosts and impossible on some embedded targets.
- **Link restic as a Go library** — restic does not offer a stable library API.

## References

- [FAQ](https://arkeep.io/docs/reference/faq/) on arkeep.io/docs.
- `agent/internal/restic/`, `agent/scripts/download_deps.go`.
