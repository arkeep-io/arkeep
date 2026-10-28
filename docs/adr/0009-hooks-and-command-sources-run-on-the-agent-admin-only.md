# 0009. Hooks and command sources run as shell commands on the agent, admin only

- **Status:** Accepted
- **Date:** 2026-10-12 (retro-documented; the decision predates this record)
- **Deciders:** Arkeep maintainers

## Context

Many backups need something run on the machine: dumping a database, stopping
and restarting a container, producing an export. Adding a dedicated source
type for each application does not scale, so Arkeep lets admins run their own
shell commands on the agent instead.

## Decision

- Policies can run shell commands on the target machine in two ways, both
  executed by the agent (`/bin/sh -c` on Unix, `cmd /C` on Windows):
  - **pre- and post-backup hooks**, with a timeout and their output captured
    in the job log. A failing pre-backup hook aborts the job. A failing
    post-backup hook is logged without changing the job's outcome;
  - **command sources** (source type `command`): restic runs the command
    itself (`--stdin-from-command`) and stores its standard output as a
    snapshot, one per command source under its own tag. Nothing is written to
    a temporary file on the agent, and a non-zero exit cancels that snapshot.
- Command sources are the recommended way to back up application dumps (for
  example `pg_dump`) rather than a hook writing a file to back up.
- Hooks and command sources are the **same trust class**: only **admins** can
  set or change them, and the server applies the same validation to both.

## Consequences

- Hooks and command sources give admins the equivalent of shell access to
  every machine running an agent, with the agent's privileges. Their
  configuration must be trusted like SSH access, and a compromised server or
  admin account can run commands on all agents.
- The server rejects some patterns at save time (command substitution, `..`,
  references to `$RESTIC_*`, `$RCLONE_*`, `$ARKEEP_*`). This is a guard against
  mistakes, not a security boundary: a shell command can always be written to
  bypass it.
- The admin-only rule is enforced twice: every policy write is admin-only at
  the router ([0008](0008-short-lived-jwt-and-refresh-cookie-sessions.md)),
  and the policy handlers check it again for hooks and command sources, in case
  policy writes are ever opened to other roles.

## Alternatives considered

- **Built-in integrations per application** — endless list, slower to evolve.
- **Hooks only, dumping to a temporary file** — the dump lands on the agent's
  disk before being backed up, which costs space and leaves sensitive data
  behind if cleanup fails.
- **Agent-side allowlist of scripts** — safer against a compromised server, but
  less convenient. It may be added later as an option.

## References

- [Security](https://arkeep.io/docs/concepts/security/) ("Hooks and command
  sources") on arkeep.io/docs; `SECURITY.md` → Hook commands.
- `agent/internal/hooks/`, `agent/internal/restic/wrapper.go`
  (`BackupFromCommand`), `server/internal/policyutil/sources.go`
  (`CommandSources`), `server/internal/api/hook_validation.go`,
  `server/internal/api/source_validation.go`.
