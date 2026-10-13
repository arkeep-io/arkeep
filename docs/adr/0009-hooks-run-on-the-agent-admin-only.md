# 0009. Hooks run as shell commands on the agent, admin only

- **Status:** Accepted
- **Date:** 2026-10-12 (retro-documented; the decision predates this record)
- **Deciders:** Arkeep maintainers

## Context

Many backups need a preparation step (dumping a database, stopping a
container) and a follow-up step. Adding a dedicated source type for each
application does not scale.

## Decision

- Policies can define **pre- and post-backup hooks**: shell commands that the
  agent runs on the target machine (`/bin/sh -c` on Unix, `cmd /C` on Windows),
  with a timeout and their output captured in the job log.
- A failing pre-backup hook aborts the job. A failing post-backup hook is
  logged without changing the job's outcome.
- Only **admins** can set or change hooks.
- Hooks are the extension point for application-specific preparation, rather
  than new source types (see `CONTRIBUTING.md`).

## Consequences

- Hooks give admins the equivalent of shell access to every machine running an
  agent, with the agent's privileges. Hook configuration must be trusted like
  SSH access, and a compromised server or admin account can run commands on
  all agents.
- The server rejects some patterns at save time (command substitution, `..`,
  references to `$RESTIC_*`, `$RCLONE_*`, `$ARKEEP_*`). This is a guard against
  mistakes, not a security boundary: a shell command can always be written to
  bypass it.

## Alternatives considered

- **Built-in integrations per application** — endless list, slower to evolve.
- **Agent-side allowlist of scripts** — safer against a compromised server, but
  less convenient. It may be added later as an option.

## References

- `SECURITY.md` → Hook commands; `README.md` → FAQ "Are pre/post backup hooks safe?".
- `agent/internal/hooks/`, `server/internal/api/hook_validation.go`.
