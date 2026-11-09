# Restore end-to-end tests — design

Date: 2026-11-07
Status: approved (2026-11-07), implemented
Branch: `test/restore-e2e` (from `develop`)

## Intent

Arkeep is about to be shown to influencers. The backup side is trusted; the
restore side is not proven: no test today runs a real `restic backup` followed
by a real `restic restore` and compares the result. Every existing restore test
checks a fragment (restic argument building, fake shell-script binaries,
include/exclude reconciliation, server API with a fake agent).

The maintainer's worst case is **in-place restore broken**, and worse still,
**in-place and restore-to-another-folder broken at the same time** — the user
then has no way out. These tests exist so that cannot ship unnoticed.

This is project 1 of 2. Project 2 (the user-facing "automated restore test"
feature from the website roadmap: restore a snapshot to a scratch location on a
schedule and verify it) is designed separately afterwards and builds on the
restore path these tests prove.

## Goals

- Real round trips with the real embedded restic binary: create files → back up
  → damage the originals (modify, delete) → restore → compare byte for byte
  (content, plus file mode and symlink targets).
- Cover **both** restore modes through the agent executor (`executeRestore`),
  i.e. starting from the job payload the server sends, not only the wrapper:
  - **restore to another folder** (`target_path` = a directory);
  - **in place** (`target_path` = `"/"`, the GUI's sentinel).
- Cover the **Docker-on-Linux deployment** first, because most users run it:
  agent with `dockerHostRoot` set, so path translation (`translateLocalPath`,
  the `"/"` no-double-nesting exception) and the lchown tolerance in
  `runRestoreJSON` are exercised. Simulated in Go tests by using a temp
  directory as the host root — no container.
- Also cover the native deployment (`dockerHostRoot` = `""`).
- Test every restore variation that can run on the maintainer's machine
  (Linux under WSL, Docker Engine available) and in CI (ubuntu runner).
  Minimum matrix:
  - whole snapshot, and selected paths (`include_paths`), both modes;
  - snapshot by ID and by `"latest"`;
  - awkward data: nested dirs, spaces and non-ASCII in names, empty files,
    empty dirs, symlinks, a file large enough to span several restic chunks
    (a few MB), restrictive permissions;
  - target folder that does not exist yet / already contains other files
    (assert and document the actual behaviour: restic does not delete extra
    files);
  - in-place with Docker-volume exclusions: volumes root not writable → whole
    root excluded; writable + running container → that volume skipped with a
    warning; include path inside an excluded volume → skipped; include path
    that is an ancestor of an excluded one → job fails with the documented
    message.
- Failure paths must **fail loudly**, never report success:
  wrong repository password, missing/corrupted repository, snapshot ID that
  does not exist, include path that matches nothing, unwritable target,
  non-lchown errors under the host root (must not be swallowed by the lchown
  tolerance), cancellation mid-restore → status `cancelled`.
- Browse and download, which the roadmap groups with restore: real-restic
  tests for `Wrapper.Ls` (listing matches what was backed up) and
  `Wrapper.Dump` (single file content equals the original; directory archive
  contains the expected entries).
- Server ↔ agent contract: the restore payload the server builds
  (`server/internal/api/snapshots.go`) must decode into the agent's
  `restorePayload` with every field populated. Today the agent struct only
  "mirrors" the server one by comment; a field rename would break restores
  silently.
- Run in the existing CI job (`go test -race` per module, 20 min timeout) with
  no new services, and locally with `task test:agent` / `task test:server`.

## Non-goals

- Remote destinations (S3, SFTP, rclone remotes, REST). Local repository only.
  Restore logic in the agent is the same for every backend; backend transport
  is restic's job.
- GUI tests for the restore flow.
- Windows and macOS agents. The Windows-specific `"Access is denied"` handling
  in `executeRestore` (logged as a warning, job reported as succeeded) cannot
  be exercised here; it is recorded below as a known risk, not fixed in this
  project. `translateLocalPath` with `C:\…` inputs is still covered because it
  is pure string logic.
- The user-facing restore-test feature (project 2).
- Option 2 (smoke test against the real agent Docker image) — the maintainer
  will run it by hand as a final check after option 1 lands; this project only
  provides a short written procedure for it (see Deliverables).

## Anti-goals

- Tests that pass because they mock the thing under test. Every round-trip test
  must invoke the real restic binary.
- Tests that are skipped silently. If restic is unavailable, or the test needs
  root/Docker and it is absent, `t.Skip` with an explicit reason; the core
  round trips (both modes, both deployments) must never skip in CI.
- Production code changed only to make tests nicer. Production changes are
  allowed only (a) to fix a real bug a test uncovers, or (b) a minimal seam
  where a hard-coded value makes a scenario impossible to test (see
  `dockerVolRoot` below). Each such change is called out in the commit list.
- Flaky or slow: the whole new suite should add well under a couple of minutes
  to the agent tests.

## Constraints

- Go workspace with three modules; tests live next to the code they test, table
  driven, standard `testing` package (CLAUDE.md).
- Restic and rclone are embedded via `agent/internal/restic/bin/` (downloaded
  by `download_deps.go`, already run in CI before tests). Tests obtain them the
  production way: `restic.NewExtractor(t.TempDir())` + `restic.NewWrapper`.
- The executor is constructed with `executor.New(wrapper, nil, nil, logger,
  dockerHostRoot, hostname)`; existing tests already use `fakeReporter` /
  `fakeSink` to capture statuses and logs — reuse them.
- Accepted ADRs apply (0003 restic/rclone embedded in the agent; 0002
  protocol). This project adds tests only and follows existing decisions, so no
  new ADR is expected unless a test forces a design change.
- Claude does not commit; the maintainer commits from the provided file list.

## Decided by the maintainer

- Do both: code-level restore tests now (this project), user-facing restore
  test feature afterwards.
- Priority scenario: in-place restore; worst case is both modes broken.
- Main deployment to protect: agent in Docker on Linux.
- Option 1 (simulate the host root inside Go tests) is the implementation;
  option 2 (real container smoke test) is a manual final check afterwards.
- Test everything that can be tested on the local machine.

## Known risks the builder must resolve first

1. **In-place restore to `"/"` as a non-root user.** In production the agent in
   Docker runs as root and the snapshot paths are `/hostfs/...`. In a test the
   snapshot paths are absolute temp paths (`/tmp/TestX/hostfs/...`), and
   `restic restore --target /` will also try to restore metadata of the parent
   directories stored in the snapshot (`/tmp`, …), which a non-root user cannot
   change. Spike this first with the real binary. Acceptable outcomes, in
   order of preference: restic tolerates it; the existing lchown tolerance
   covers it (only if that matches production behaviour, not by widening the
   tolerance); the in-place tests that need it run only as root (`t.Skip`
   otherwise) and the maintainer runs them as root locally and they run as root
   in a dedicated CI step. If the spike shows in-place restore is actually
   broken in a way users would hit, that is a bug to report to the maintainer
   before working around it.
2. **`dockerVolRoot` is a hard-coded constant** (`/var/lib/docker/volumes`),
   read by `buildInPlaceExcludes` via `os.Stat`/`os.CreateTemp`. The
   Docker-volume exclusion scenarios cannot point at a temp dir. Smallest
   acceptable seam: make it a field on `Executor` defaulting to the constant
   (or a package var), set only by tests. The "running container" scenario also
   needs a Docker client; with Docker Engine available it can use a real
   container (skip with reason if Docker is absent), otherwise test the
   exclusion list via the existing pure functions.
3. Windows "Access is denied" → reported as success (see Non-goals). Record in
   the final report as an open finding for the maintainer.
4. **Docker volumes reached through the host root are not excluded.** The
   compose files mount volumes twice: `/var/lib/docker/volumes` at the same
   path, and again inside `/:/hostfs`. `buildInPlaceExcludes` and the
   include-overlap check compare against the untranslated `dockerVolRoot`
   only. A user who backs up `/var/lib` or `/` as a path source gets
   `/hostfs/var/lib/docker/volumes/...` in the snapshot; an in-place restore
   (with `/hostfs` mounted `:rw`) then writes into the volumes of running
   containers, and an include of `/hostfs/var` never triggers the ancestor
   check. Write the failing test that shows it first. Whether to fix it on this
   branch: see "Open decision" below.
5. **The lchown tolerance is only tested in the negative direction**
   (non-lchown errors under the host root are not swallowed); the tolerated
   case needs root or NTFS and is not exercised.

## Calls made by Claude (not decided by the maintainer)

- **Docker client seam.** `Executor.docker` is a concrete `*docker.Client`.
  Allowed: a narrow interface covering only the methods the executor calls
  (`ListRunningContainerVolumes`, `InspectVolume`), so the "running container
  → volume skipped" scenario can run through `executeRestore` with a fake
  reporting temp-dir mountpoints under the `dockerVolRoot` seam. Together with
  `dockerVolRoot`, this is the only testability seam. One extra test uses the
  real Docker Engine (native engine in WSL / CI runner) and skips with a reason
  when Docker or root is absent.
- **lchown tolerance.** It does not need to be shown accepting a real restic
  restore on Linux (it cannot fire for files the test user owns). Required:
  a real-restic test proving non-lchown errors under the host root still fail
  the job, plus the existing string-parsing tests.
- **Payload contract.** "Every field populated" means: every field the server
  sends decodes into a non-zero agent field, and every field `executeRestore`
  reads is sent by the server. Fields the server never sends for restores
  (`credentials`, `config`, `priority`, `repo_must_exist`) are out of the check.
  The same contract test covers the browse/download payload
  (`snapshotRepoAccess` ↔ the connection manager's mirror).
- **Fixture placement.** Docker-volume fixtures sit under the `dockerVolRoot`
  seam (as with `docker-volume://` sources) **and** a second case under the
  simulated host root (as with a `/` or `/var/lib` path source), to exercise
  risk 4.

## Open decision (maintainer)

When a test exposes a real restore bug users would hit (risk 4 is the first
candidate; the risk-1 spike may add more): fix it on this branch, or stop and
report? — **Decided: fix it on this branch**, test first (failing test, then
the fix), each fix listed separately in the commit list. The final report ends
with a recap of every bug fixed.

## Deliverables

- New test files in `agent/internal/executor/` (round trips through
  `executeRestore`) and `agent/internal/restic/` (real-binary `Restore`, `Ls`,
  `Dump`, failure paths), with a small shared helper to build a fixture tree,
  back it up and compare two trees.
- A contract test for the restore payload (mechanism is the builder's call,
  e.g. a golden JSON fixture produced by the server test and decoded by the
  agent test).
- Any minimal production seam or bug fix, each listed separately.
- `docs/operations/` (or the existing operations doc that fits): a short manual
  procedure for option 2 — run the published agent image with the compose
  mounts users use (note `/:/hostfs:ro` must be `:rw` for in-place restore),
  back up a test folder, restore in place and to a folder, compare.
- Final report: test matrix with pass/skip per scenario, bugs found, open risks.

## Left to the builder

- File layout and helper names; how fixtures and comparisons are implemented.
- Whether failure-path tests live at wrapper or executor level (prefer executor
  where the status reported to the server is what matters).
- How to corrupt a repository for the "corrupted repo" case.
- Exact chunk-spanning file size.
