# 0011. Go monorepo, Conventional Commits and GoReleaser releases

- **Status:** Accepted
- **Date:** 2026-10-12 (retro-documented; the decision predates this record)
- **Deciders:** Arkeep maintainers

## Context

The server, the agent and the gRPC contract evolve together and are released
together. Contributors need one place to work, and releases must produce
binaries for many platforms, container images and packages without manual
steps.

## Decision

- **One repository** with three Go modules (`server`, `agent`, `shared`) tied by
  `go.work`, plus the GUI in `gui/`. `shared` holds the protobuf contract and
  types used by both sides. Each module also builds alone (`GOWORK=off`).
- Development tasks are defined in `Taskfile.yml` (Task).
- Commit messages follow **Conventional Commits**. Their prefixes drive the
  release changelog groups.
- Releases are cut by pushing a `v*` tag. **GoReleaser** then builds the server
  and agent binaries for every supported platform, multi-arch images on GHCR
  and Docker Hub, the AUR package, `checksums.txt` and the GitHub release
  notes.

## Consequences

- A change to the gRPC contract updates both sides in a single pull request.
- The server and agent share a version number. Mixed versions only exist
  during upgrades.
- The release pipeline depends on GoReleaser's configuration, which must be
  reviewed like code.

## Alternatives considered

- **Separate repositories for server and agent** — cross-repository changes
  for every contract update.
- **Hand-written release scripts** — more code to maintain for multi-platform
  builds and packaging.

## References

- `go.work`, `Taskfile.yml`, `.goreleaser.yml`, `.github/workflows/release.yml`.
- `CONTRIBUTING.md` → Commits.
