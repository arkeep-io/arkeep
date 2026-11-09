# Restore End-to-End Tests Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove with the real restic binary that restore works in both modes (to a folder, in place) on the Docker-on-Linux and native agent, fail loudly on every error path, and fix the restore bugs the tests expose.

**Architecture:** A small test-helper package (`agent/internal/restic/restictest`) builds a fixture tree, extracts the embedded restic once per test binary, and compares trees. Wrapper-level tests cover `Restore`/`Ls`/`Dump`; executor-level tests drive `executeBackup` → damage → `executeRestore` from the JSON payloads the server sends, with `dockerHostRoot` pointing at a temp dir to simulate `/hostfs`. Two minimal seams (`Executor.dockerVolRoot`, a `dockerAPI` interface) make the Docker-volume scenarios testable. Golden JSON files in `shared/testdata/contract/` pin the server ↔ agent payloads.

**Tech Stack:** Go 1.x workspace (agent/server/shared modules), standard `testing`, embedded restic 0.19.1, zap.

**Spec:** `docs/superpowers/specs/2026-11-07-restore-e2e-tests-design.md`

## Global Constraints

- Every round-trip test runs the **real** embedded restic, obtained via `restic.NewExtractor` + `restic.NewWrapper` (never a fake binary).
- Local repositories only; no S3/SFTP/rclone remotes, no GUI, no Windows/macOS runs.
- Production changes allowed only: the two seams in Task 2, and fixes for bugs a failing test exposes (Task 5, plus any found elsewhere — test first, then fix, listed separately).
- Skips: `t.Skip` with an explicit reason only for root-only, non-root-only or Docker-only scenarios. The core round trips (both modes × native/Docker host root) never skip.
- The suite must run in the existing CI step (`cd agent && go test -v -race -count=1 -timeout 20m ./...`) and add well under 2 minutes.
- Go conventions from CLAUDE.md: lowercase error strings, English comments, table-driven tests where it fits.
- **No `git commit` by Claude.** Each task ends by appending its files to the commit list handed to the maintainer, with a Conventional Commit message (`test:` for tests, `fix:` for bug fixes, `refactor:` for seams). No Co-Authored-By line.
- Spike result (2026-11-07, restic 0.19.1, uid 1000): `restic restore --target / latest` of a snapshot under `/tmp/...` exits 0 with no stderr and restores modified/deleted files. In-place tests therefore run as non-root.

## Review Focus

- **Restore-to-folder layout with a host root.** restic recreates the snapshot's absolute paths under the target, so with `dockerHostRoot` set, files land at `<target>/<hostRoot>/home/u/data/...` (e.g. `/home/u/restore/hostfs/home/u/data` on the host). Pin the actual layout in Task 3 so a change is deliberate.
- **Include paths taken from browse.** The GUI sends include paths exactly as `Ls` returns them (snapshot-internal, `/hostfs/...` form, not host form). Task 3 chains `Wrapper.Ls` → pick a path → restore with that include.
- **Many files.** `runRestoreJSON` must drain stdout so restic never stalls; Task 3 restores a tree with 3000 small files.
- **In-place with `/hostfs` writable and Docker volumes reachable through it** (risk 4) — Task 5.
- **Extra files in the target** are kept by restic (no delete); Task 3 pins it so users' expectations are documented.

---

### Task 1: `restictest` helpers + real-binary wrapper round trips

**Files:**
- Create: `agent/internal/restic/restictest/restictest.go`
- Create: `agent/internal/restic/restore_e2e_test.go` (package `restic_test`)
- Create: `agent/internal/restic/main_test.go` (package `restic_test`, `TestMain`)

**Interfaces:**
- Produces (package `restictest`):
  - `func Wrapper(t testing.TB) *restic.Wrapper` — extracts the embedded binaries once per test process (`sync.Once`, dir from `os.MkdirTemp("", "arkeep-restictest-*")`), returns a wrapper with `zap.NewNop()`; fails the test if extraction fails.
  - `func Cleanup()` — removes the extraction dir; called from each package's `TestMain` after `m.Run()`.
  - `func NewLocalRepo(t testing.TB) restic.Destination` — `Type: restic.DestLocal`, `RepoURL` = `t.TempDir()+"/repo"`, `Password: "test-password"`; not initialised (backup creates it).
  - `func WriteTree(t testing.TB, root string)` — the fixture below.
  - `func DamageTree(t testing.TB, root string)` — overwrites `a.txt` with `"damaged\n"`, deletes `nested/deep/er/file.bin` and `link-to-a`, chmods `private.txt` to 0644, creates `extra.txt`.
  - `type Entry struct { Type string; Mode fs.FileMode; SHA256 string; Link string; ModTime int64 }` (`Type` ∈ `file|dir|symlink`; `ModTime` Unix seconds, files only).
  - `type Tree map[string]Entry` keyed by slash-separated path relative to the root.
  - `func Capture(t testing.TB, root string) Tree`
  - `func AssertTreeMatches(t testing.TB, want Tree, root string, extra ...string)` — `Capture(root)` must equal `want` plus exactly the `extra` paths; reports every difference, not just the first.
- Fixture (`WriteTree`), exact contents:
  - `a.txt` = `"hello arkeep\n"`
  - `dir with space/ünïcödé.txt` = `"unicode\n"`
  - `nested/deep/er/file.bin` = 8 MiB from `rand.New(rand.NewPCG(1, 2))` (spans several restic chunks)
  - `empty.txt` (0 bytes), `empty-dir/` (empty)
  - `link-to-a` → symlink to `a.txt` (relative)
  - `private.txt` mode 0600, `private-dir/` mode 0700 containing `inside.txt`
  - `many/f0000.txt` … `many/f2999.txt`, each `"<n>\n"`

- [ ] **Step 1: Write the failing tests** in `restore_e2e_test.go` (each backs up a fresh `WriteTree` with `w.Backup(ctx, dest, restic.BackupOptions{Sources: []string{src}}, nil)` and takes the snapshot ID from the result):
  - `TestRestore_ToFolder_WholeSnapshot` — restore by snapshot ID into `t.TempDir()`; `AssertTreeMatches(want, filepath.Join(target, src))`.
  - `TestRestore_ToFolder_Latest` — same with `"latest"`.
  - `TestRestore_ToFolder_IncludeFromLs` — `w.Ls(ctx, dest, id, src)` → take the entry for `dir with space`; restore with `includePaths = []string{thatEntry.Path}`; only that subtree exists under the target.
  - `TestRestore_InPlace` — `DamageTree(src)`; `w.Restore(ctx, dest, id, "/", nil, nil, "")`; `AssertTreeMatches(want, src, "extra.txt")`.
  - `TestRestore_IncludeMatchesNothing` — include `src+"/does-not-exist"` → `errors.Is(err, restic.ErrNothingRestored)`.
  - `TestLs_ListsBackedUpEntries` — `Ls` of `src` returns exactly the top-level fixture names.
  - `TestDump_File` — `Dump(..., src+"/nested/deep/er/file.bin", false, &buf)`; sha256 equals the fixture's.
  - `TestDump_DirArchive` — `Dump(..., src+"/private-dir", true, &buf)`; `zip.NewReader` lists `inside.txt` with the right content.

- [ ] **Step 2: Run** `cd agent && go test -race -count=1 ./internal/restic/ -run 'TestRestore_|TestLs_|TestDump_' -v` — Expected: FAIL to compile (`restictest` undefined).
- [ ] **Step 3: Implement `restictest`** with the signatures above. `Capture` uses `filepath.WalkDir` + `os.Lstat`; it ignores the root's own entry.
- [ ] **Step 4: Run** the command from Step 2 — Expected: PASS. Then `cd agent && go test -race -count=1 ./internal/restic/` — all PASS.
- [ ] **Step 5: Commit list** — `test: add real-restic restore, browse and download round trips`.

### Task 2: Executor seams (no behaviour change)

**Files:**
- Modify: `agent/internal/executor/executor.go` (Executor struct ~191, `New` ~228, `buildInPlaceExcludes` ~1216, `executeRestore` ~1100, `resolveSources` ~1291, const `dockerVolRoot` ~1147)
- Modify: `agent/internal/executor/executor_test.go` (fakeReporter)
- Create: `agent/internal/executor/main_test.go` (`TestMain` calling `restictest.Cleanup()`)

**Interfaces:**
- Produces:
  - `type dockerAPI interface { InspectVolume(ctx context.Context, name string) (*docker.VolumeInfo, error); ListRunningContainerVolumes(ctx context.Context) ([]docker.RunningContainerVolumes, error) }`; `Executor.docker dockerAPI`.
  - `New` keeps its signature (`dockerClient *docker.Client`) and assigns `e.docker` **only when `dockerClient != nil`** — a nil `*docker.Client` stored in the interface would make every `e.docker == nil` check false.
  - Const renamed `defaultDockerVolRoot = "/var/lib/docker/volumes"`; field `Executor.dockerVolRoot string` set to it in `New`; every former use of the const reads `e.dockerVolRoot`.
  - `fakeReporter` gains `snapshotIDs map[string]string` filled in `ReportDestinationResult`.
  - New in `executor_test.go`: `type recordingSink struct { mu sync.Mutex; logs []string; onLog func(level, msg string) }` implementing `SendLog`; `func (s *recordingSink) contains(substr string) bool`.

- [ ] **Step 1: Write the failing test** `TestNew_NilDockerClientLeavesInterfaceNil` — `New(nil, nil, nil, zap.NewNop(), "", "").docker == nil`, and `.dockerVolRoot == "/var/lib/docker/volumes"`.
- [ ] **Step 2: Run** `cd agent && go test ./internal/executor/ -run TestNew_ -v` — Expected: FAIL to compile.
- [ ] **Step 3: Implement** the seams above.
- [ ] **Step 4: Run** `cd agent && go test -race -count=1 ./internal/executor/` — all existing tests PASS.
- [ ] **Step 5: Commit list** — `refactor: make the executor's docker client and volumes root injectable for tests`.

### Task 3: Executor round trips — both modes, native and Docker host root

**Files:**
- Create: `agent/internal/executor/restore_e2e_test.go`

**Interfaces:**
- Consumes: `restictest.*` (Task 1); `fakeReporter.snapshotIDs`, `recordingSink`, `Executor.docker`/`dockerVolRoot` (Task 2).
- Produces (test helpers in this file, used by Tasks 4–6):
  - `type e2eEnv struct { e *Executor; hostRoot string; repoURL string /* as the server sends it, host form */ }`
  - `func newE2EEnv(t *testing.T, hostRoot bool) *e2eEnv` — `hostRoot=true` → `hostRoot = t.TempDir()+"/hostfs"`, executor built with it; repo URL `"/backups/repo"` (translated to `<hostRoot>/backups/repo` by the agent); `hostRoot=false` → native, repo URL `t.TempDir()+"/repo"`.
  - `func (env *e2eEnv) hostPath(p string) string` — where host path `p` lives on disk in the test (`translateLocalPath(p, env.hostRoot)`).
  - `func (env *e2eEnv) backup(t *testing.T, sources ...string) string` — runs `executeBackup` with `backupPayload{Sources: json([...]), RepoPassword: "test-password", Destinations: []destinationPayload{{DestinationID: "dest-1", Type: "local", RepoURL: env.repoURL}}}`; fails unless final status is `succeeded`; returns `reporter.snapshotIDs["dest-1"]`.
  - `func (env *e2eEnv) restore(t *testing.T, ctx context.Context, p restorePayload, sink *recordingSink) *fakeReporter` — marshals `p` (Destination filled from env), runs `executeRestore`, returns the reporter.
- Source host path for all tests: `"/home/u/data"`; fixture written at `env.hostPath("/home/u/data")`.

- [ ] **Step 1: Write the tests**, table-driven over `hostRoot ∈ {false, true}` (subtests `native` / `docker`):
  - `TestRestoreE2E_ToFolder` — backup; restore with `TargetPath: "/home/u/restore"`; final status `succeeded`; `AssertTreeMatches(want, <restored location>)` where the restored location is `env.hostPath("/home/u/restore") + <snapshot path of the source>` — snapshot path = `env.hostPath("/home/u/data")` (pins the Review Focus layout).
  - `TestRestoreE2E_ToFolder_TargetMissingAndExisting` — target dir absent → created; target already holding `keep.txt` → restored tree matches and `keep.txt` still there.
  - `TestRestoreE2E_InPlace` — backup; `DamageTree`; restore with `TargetPath: "/"`; `succeeded`; `AssertTreeMatches(want, env.hostPath("/home/u/data"), "extra.txt")`.
  - `TestRestoreE2E_InPlace_Include` — include = snapshot path of `dir with space` (from `Wrapper.Ls`); damage `a.txt` and the unicode file; after restore the unicode file is back, `a.txt` stays damaged.
  - `TestRestoreE2E_Latest` — `ResticSnapshotID: "latest"` in-place, `succeeded`.
  - `TestRestoreE2E_BothModesSameSnapshot` — one backup, then in-place restore and to-folder restore of the same snapshot both `succeeded` and match (the maintainer's worst case).
- [ ] **Step 2: Run** `cd agent && go test -race -count=1 ./internal/executor/ -run TestRestoreE2E_ -v` — Expected: PASS. Any FAIL is a real bug: write it up, fix it test-first (separate `fix:` entry), re-run.
- [ ] **Step 3: Commit list** — `test: add executor restore round trips for in-place and folder targets`.

### Task 4: Failure paths fail loudly

**Files:**
- Create: `agent/internal/executor/restore_failures_e2e_test.go`

**Interfaces:**
- Consumes: `e2eEnv`, `backup`, `restore` (Task 3).

- [ ] **Step 1: Write the tests** (docker host root unless noted); each asserts the **final** reported status and that no status `succeeded` was reported:
  - `TestRestoreE2E_WrongPassword` → `failed`.
  - `TestRestoreE2E_MissingRepository` → `failed` (repo URL pointing to an empty dir).
  - `TestRestoreE2E_CorruptedRepository` → `failed` (after backup, delete every file under `<repo>/data/`).
  - `TestRestoreE2E_UnknownSnapshotID` → `failed` (`"deadbeef"`).
  - `TestRestoreE2E_IncludeMatchesNothing` → `failed`, last message contains `nothing in the snapshot matched`.
  - `TestRestoreE2E_UnwritableTarget` (skip as root: root bypasses mode bits) → target parent chmod 0555 → `failed`; covers "non-lchown errors under the host root are not swallowed".
  - `TestRestoreE2E_CancelledMidRestore` → sink `onLog` cancels the ctx when a log contains `restore started`; final status `cancelled`.
- [ ] **Step 2: Run** `cd agent && go test -race -count=1 ./internal/executor/ -run 'TestRestoreE2E_(Wrong|Missing|Corrupted|Unknown|IncludeMatches|Unwritable|Cancelled)' -v` — Expected: PASS; any FAIL is a bug → fix test-first, separate `fix:` entry.
- [ ] **Step 3: Commit list** — `test: cover restore failure paths with real restic`.

### Task 5: Docker volumes reached through the host root (bug, risk 4)

**Files:**
- Create: `agent/internal/executor/restore_docker_volumes_e2e_test.go`
- Modify: `agent/internal/executor/executor.go` (`buildInPlaceExcludes`, the overlap check in `executeRestore`)

**Interfaces:**
- Consumes: `e2eEnv` (Task 3), `dockerAPI`, `Executor.dockerVolRoot` (Task 2).
- Produces:
  - `type fakeDocker struct { running []docker.RunningContainerVolumes }` (test file) implementing `dockerAPI`.
  - `func (e *Executor) dockerVolRoots() []string` — `[e.dockerVolRoot]`, plus `translateLocalPath(e.dockerVolRoot, e.dockerHostRoot)` when `dockerHostRoot != ""` (the alias under `/hostfs`).
- Required behaviour after the fix (the tests are the spec):
  - For **each** root in `dockerVolRoots()` that exists: not writable → excluded whole (with the existing warning); writable → nothing excluded for that root unless Docker reports running containers.
  - Every running-container volume path `p` (Docker reports host form under `e.dockerVolRoot`) is excluded as `p` **and** as its alias (`aliasRoot + strings.TrimPrefix(p, e.dockerVolRoot)`), one warning per volume.
  - `executeRestore` runs the include reconciliation when an include overlaps **any** root in `dockerVolRoots()`.
- Setup for all tests: docker host root; `e.dockerVolRoot = t.TempDir()+"/vols"`; volume `app_data` at `<root>/app_data/_data/db.txt`.

- [ ] **Step 1: Write the failing tests**:
  - `TestInPlace_RunningVolumeViaHostRootIsSkipped` — fixture written under the alias (`env.hostPath(e.dockerVolRoot)+"/app_data/_data/db.txt"`) plus a normal file under `/home/u/data`; backup both as host-path sources (as a user backing up `/var/lib` would); `fakeDocker` reports container `app` with `<dockerVolRoot>/app_data/_data`; damage both; in-place restore → `succeeded`, normal file restored, `db.txt` **still damaged**, sink contains `container "app" is running`.
  - `TestInPlace_IncludeInsideAliasVolumeIsSkipped` — include = alias path of `db.txt` plus the normal file → `db.txt` untouched, warning `skipping`, normal file restored.
  - `TestInPlace_IncludeAncestorOfAliasVolumeFails` — include = alias of `<dockerVolRoot>` parent → `failed`, message contains `cannot restore`.
  - `TestInPlace_ReadOnlyVolumesRootExcluded` (skip as root) — direct root and alias chmod 0555 after backup → `succeeded`, warning `not writable`, volume data untouched.
  - `TestInPlace_DockerVolumeSourceStillSkipped` (regression) — data directly under `e.dockerVolRoot`, backed up as that path, running container → skipped.
- [ ] **Step 2: Run** `cd agent && go test -race -count=1 ./internal/executor/ -run TestInPlace_ -v` — Expected: the first three FAIL (volume overwritten / no failure), last two PASS.
- [ ] **Step 3: Implement** `dockerVolRoots()` and the behaviour above in `buildInPlaceExcludes` and `executeRestore`; update the warning text only where it names a single path.
- [ ] **Step 4: Run** the Step 2 command — all PASS; then `cd agent && go test -race -count=1 ./...` — all PASS.
- [ ] **Step 5: Commit list** — `fix: exclude docker volumes reached through the host root from in-place restore` (code) and `test: ...` (tests) as separate entries.

### Task 6: Real Docker Engine scenario (gated)

**Files:**
- Create: `agent/internal/executor/restore_real_docker_e2e_test.go`

**Interfaces:**
- Consumes: `e2eEnv` (native, `hostRoot=false`), `docker.NewClient` from `agent/internal/docker`.

- [ ] **Step 1: Write** `TestInPlace_RealDocker_RunningContainerVolumeSkipped` — skip with reason unless `os.Geteuid() == 0`, `docker` is on `PATH`, and the client pings. Using the `docker` CLI: create volume `arkeep-e2e-<random>`; run a container from a busybox image **pinned by digest** (ADR 0013) with the volume at `/data`, `sleep 300`; skip if the pull fails. Write `db.txt` in the volume mountpoint; backup with source `docker-volume://<name>`; damage `db.txt`; in-place restore with the real client → `succeeded`, `db.txt` still damaged, warning logged. `t.Cleanup` removes container and volume.
- [ ] **Step 2: Run** as the maintainer locally: `cd agent && sudo -E go test -count=1 ./internal/executor/ -run TestInPlace_RealDocker -v` — Expected: PASS. In CI (non-root) — SKIP with reason.
- [ ] **Step 3: Commit list** — `test: cover in-place restore against a real running container volume`.

### Task 7: Server ↔ agent payload contract

**Files:**
- Create: `shared/testdata/contract/restore.json`, `browse.json`, `download.json` (generated by the server test)
- Create: `server/internal/api/payload_contract_test.go`
- Create: `agent/internal/executor/payload_contract_test.go`
- Create: `agent/internal/connection/payload_contract_test.go`

**Interfaces:**
- Server test builds `restorePayload`, `snapshotBrowsePayload`, `snapshotDownloadPayload` with every field non-zero (`IncludePaths` one entry, `Env` one key, `Archive: true`), marshals them, and compares to the golden files as JSON (not bytes); flag `-update-contract` rewrites them. Path: `../../../shared/testdata/contract/`.
- Agent tests decode the golden files with `json.Decoder.DisallowUnknownFields()` into `restorePayload` / `snapshotBrowsePayload` / `snapshotDownloadPayload` (a field the server adds and the agent ignores fails the test) and assert every field `executeRestore` and the connection manager read is non-zero: `ResticSnapshotID`, `RepoPassword`, `TargetPath`, `IncludePaths`, `Destination.{Type,RepoURL,Env}`, browse `Path`, download `Path`, `Archive`. Fields the server never sends for these jobs (`credentials`, `config`, `priority`, `repo_must_exist`) are not checked.

- [ ] **Step 1: Write** the server test; run `cd server && go test ./internal/api/ -run TestPayloadContract -update-contract` to generate the golden files; inspect them.
- [ ] **Step 2: Run** `cd server && go test ./internal/api/ -run TestPayloadContract -v` — PASS.
- [ ] **Step 3: Write** both agent tests; run `cd agent && go test ./internal/executor/ ./internal/connection/ -run PayloadContract -v` — PASS. Sanity check: rename a json tag locally in the agent struct → FAIL; revert.
- [ ] **Step 4: Commit list** — `test: pin the restore, browse and download payloads between server and agent`.

### Task 8: Manual smoke procedure (option 2) and final report

**Files:**
- Create: `docs/operations/restore-smoke-test.md`

- [ ] **Step 1: Write the procedure**: run the published agent image with the user-facing compose mounts (`/:/hostfs:rw` — note the default `:ro` blocks in-place restore — `/var/lib/docker/volumes:/var/lib/docker/volumes:ro`, docker socket); create `/tmp/arkeep-smoke/data` on the host; back it up through the GUI to a local destination; damage it; restore in place and to `/tmp/arkeep-smoke/restore`; compare with `diff -r` against a copy made before damaging; also restore a running container's volume and confirm it is skipped.
- [ ] **Step 2: Run the full suites**: `cd agent && go test -race -count=1 -timeout 20m ./...`, `cd server && go test -race -count=1 -timeout 20m ./...` (background, 20-minute timeout) — all PASS.
- [ ] **Step 3: Final report to the maintainer** (in Italian): scenario matrix (pass/skip with reason), **recap of every bug fixed**, open risks (Windows "Access is denied" reported as success), and the complete commit list with messages.
