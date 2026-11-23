# 0026. Repair repositories from the GUI under a destination maintenance mode

- **Status:** Proposed
- **Date:** 2026-11-21
- **Deciders:** Arkeep maintainers

## Context

A restic repository can lose pack files or index entries: a storage backend
that loses blocks, an interrupted upload, a disk error. Integrity checks
([0010](0010-per-destination-locks-and-fifo-queue.md), issue #307) report the
damage, but Arkeep offers no way to repair it. The admin has to install
restic elsewhere, rebuild the repository URL, password and backend
credentials by hand, and follow restic's procedure: `repair index`, a new
backup of the affected sources, `repair snapshots --forget`, `check
--read-data`.

Meanwhile Arkeep keeps running scheduled backups, retention sweeps and checks
against the repository being repaired. Some repair commands change the
repository for good: `repair snapshots --forget` rewrites snapshots without
the files whose data is lost, and `repair packs` replaces damaged pack files.

## Decision

- A new job type, **repair**, runs one restic repair command on the
  destination's **maintenance agent**, the agent that already runs its
  retention sweeps and integrity checks. It reuses the paths of those jobs: a
  job record, the job log stream, the job page, and the destination payload
  with the secrets in the process environment.
- Repair actions:
  - `unlock`: `restic unlock`, which removes stale locks only;
  - `repair-index`: `restic repair index`, optionally `--read-all-packs`;
  - `repair-snapshots`: `restic repair snapshots`, a dry run unless asked
    otherwise, optionally `--forget`, filtered by host, tag or snapshot ID;
  - `repair-packs`: `restic repair packs <ids>`;
  - `find-packs`: `restic find --pack <ids>`, the read-only preview of
    `repair-packs`, which has no dry run;
  - `check`: `restic check`, structural or with `--read-data`, so that a
    check can run while the destination is in maintenance.

  Arguments are built as a list, never through a shell, and every value that
  comes from the user is validated: hexadecimal IDs, and a restricted
  character set for hosts and tags. restic's output is streamed to the job
  log line by line while it runs.
- **Repairs are admin only**, like every other maintenance action. The two
  destructive actions, `repair-snapshots --forget` and `repair-packs`, need
  the destination's name in the request, which the GUI asks the admin to
  type after showing the preview. The job records who started it and its
  parameters, and the audit log records the request.
- **A destination can be put in maintenance mode**, by an admin, or
  automatically for the duration of a repair started outside of it. While a
  destination is in maintenance, no job can claim it: backups, retention
  sweeps and checks that need it wait in the queue of
  [0010](0010-per-destination-locks-and-fifo-queue.md) and start, in order,
  once the maintenance ends. A job with several destinations waits as a
  whole. Jobs already running finish normally: the maintenance becomes
  effective once the destination holds no lock.
- Maintenance is a state of the destination, not a job: it survives server
  restarts and agent disconnections, which interrupt running jobs.
- **A repair only starts on a destination in effective maintenance**, and
  only one at a time. Repairs are not queued: when the destination is busy
  the request is refused, rather than run later unattended. A repair started
  on a destination that is not in maintenance and holds no lock puts it in
  maintenance for the duration of the job.
- An agent reports, through a new `AgentCapabilities.repair` field, that its
  restic supports the repair commands (0.16 or later). The server refuses a
  repair on a maintenance agent that does not report it, and the GUI disables
  the repair actions. An agent that does not know the new job type rejects
  it, as it does for any unknown type.

## Consequences

- Repairs no longer need restic, the repository password or the backend
  credentials outside Arkeep, nor a machine that can reach the storage: the
  maintenance agent already can.
- The repair procedure can run without backups or sweeps racing it, and no
  scheduled run is lost: the queue catches up when the maintenance ends.
  The scheduler's rule of one waiting backup per policy, and one check or
  sweep per destination, keeps the catch-up to one run each.
- Jobs that wait for a maintenance longer than the queue timeout fail, as
  jobs waiting for a busy destination already do.
- Restores, snapshot browsing and downloads do not claim destinations and
  are not stopped by the maintenance mode.
- The admission of every destination claim also checks the maintenance mode,
  in the same transaction.

## Alternatives considered

- **Skipping scheduled jobs during maintenance** — the backups of that period
  would be lost; waiting reuses the queue and its catch-up.
- **Maintenance as a job holding an exclusive lock** — reuses the lock table
  as it is, but running jobs are interrupted when their agent disconnects or
  the server restarts, which would end a maintenance at the worst moment.
- **Queueing repairs like retention and checks** — a destructive repair could
  start long after it was requested, once the situation has changed.
- **Running repairs on the server** — the server has no restic
  ([0003](0003-restic-and-rclone-embedded-in-the-agent.md)) and cannot always
  reach a destination that only an agent sees.

## References

- restic troubleshooting guide:
  <https://restic.readthedocs.io/en/stable/077_troubleshooting.html>
- `server/internal/destqueue`, `server/internal/repositories/destination.go`
  (`TryAcquireBusyAll`), `agent/internal/restic`
