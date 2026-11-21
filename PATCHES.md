# PunterDigital patches

This is PunterDigital's build of [Arkeep](https://github.com/arkeep-io/arkeep), released as
`vX.Y.Z-pd.N` on top of upstream `vX.Y.Z`. This file lists everything that differs from
upstream (Apache-2.0, section 4(b): modified files carry this notice).

## Build and release (no change to the app)

| Change | Files |
|---|---|
| Our own release: GHCR images under `ghcr.io/punterdigital`, GitHub Releases on this repo, `-pd.N` tags | `.goreleaser.pd.yml`, `.github/workflows/release-pd.yml` (new) |
| Playwright smoke test of the built server and GUI, plus a check of the release config | `e2e/`, `.github/workflows/pd-ci.yml` (new) |
| Fork notes and these docs | `CLAUDE.md`, `PATCHES.md` (new), a note at the top of `README.md` |

Upstream's `release.yml`, `registry-metadata.yml`, `sponsors.yml` and `deps-pins.yml` are
disabled on this fork (repository setting; the files are unchanged).

## App changes

Each one gets a row here: what it does, why, the files it touches, and the
upstream PR if it was sent upstream (cards tagged `upstream` are; see `CLAUDE.md`). A row
leaves this table once an upstream release includes the change.

| Change | Why | Files | Upstream |
|---|---|---|---|
| Test suites run on Windows and as root: Vitest resolves public-dir assets (`/icons/…`, `/login-bg.mp4`) to a module exporting the URL; the agent's read-only-leaf test skips under root | 3 GUI test files never loaded on Windows; the agent test failed in a root container (card #4560). Tests only, no app change | `gui/vitest.config.ts`, `agent/internal/executor/executor_test.go` | [arkeep-io/arkeep#495](https://github.com/arkeep-io/arkeep/pull/495) (open) |
| A folder restore into the original location of the restored paths (or a folder above it) is refused: 400 from the server (include paths, or the snapshot's sources), and the agent checks again after `/hostfs` translation. The error points to "Original location" | restic recreates the full path below the target, so such a restore nested a copy (`…/volumes/X/var/lib/docker/volumes/X/_data`), left the originals unchanged and reported success (DR trial, card #4569) | `server/internal/api/restore_target.go` (new), `server/internal/api/snapshots.go`, `agent/internal/executor/executor.go`, tests | Not sent yet (branch `fix/restore-nested-target`; upstream issue first, per their CONTRIBUTING) |
| Inside a container, a backup to a local destination that isn't on a bind mount or volume (the container's own filesystem) or that is on a tmpfs fails for that destination, with an error naming the path and how to mount it. Detected from `/proc/self/mountinfo`; native agents unchanged | Such a backup passed the write test and reported success, but the repository would be lost when the container was recreated (DR trial, card #4568) | `agent/internal/executor/durability.go` (new), `agent/internal/executor/executor.go`, tests | Not sent yet (branch `fix/non-durable-local-destination`; upstream issue first) |
