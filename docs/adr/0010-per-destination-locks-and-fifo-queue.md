# 0010. Per-destination locks and a FIFO waiting queue

- **Status:** Accepted
- **Date:** 2026-10-12 (retro-documented; the decision predates this record)
- **Deciders:** Arkeep maintainers

## Context

Several policies and agents can write to the same restic repository. Backups
can run side by side, but retention (`forget --prune`) and integrity checks
need exclusive access. Skipping a job whenever its repository is busy loses
backups. Running everything concurrently corrupts the work or fails on restic
locks.

## Decision

- Every job holds a **lock per destination** it uses (`destination_locks`):
  **shared** for backups, **exclusive** for retention sweeps and integrity
  checks.
- A job that cannot claim all of its destinations moves to the **`waiting`**
  status instead of being skipped.
- Waiting jobs form a **FIFO queue per destination**: a job cannot claim a
  destination that an older waiting job needs in a conflicting mode, so
  exclusive jobs are never starved.
- A job claims **all of its destinations in one transaction, or none**, which
  rules out deadlocks.
- The queue is drained on events that may free a destination, plus a fallback
  ticker.
- A **queue timeout** (Settings → Job Queue, 24 h by default, 0 disables it)
  fails jobs that have not started in time, whether they are `waiting` for
  their destinations or `pending` for their agent. A timed-out job is marked
  failed and pings Healthchecks. It also sends the job-failed notification,
  except when its agent is offline: the agent-offline notification already
  covers that case.

## Consequences

- No backup is lost because a repository was busy, and maintenance jobs always
  get their turn.
- Lock state lives in the database, so it survives restarts and stale locks
  must be detected and released.
- Job status gains `waiting` and the GUI must explain why a job is waiting.

## Alternatives considered

- **Skip the job when the destination is busy** — the original behavior;
  silently loses backups.
- **Rely on restic's own repository locks** — jobs fail instead of waiting, and
  restic does not order them fairly.

## References

- `server/internal/destqueue/`, `server/internal/repositories/destination.go`.
- Issues #130, #285, #319, #325, #345.
