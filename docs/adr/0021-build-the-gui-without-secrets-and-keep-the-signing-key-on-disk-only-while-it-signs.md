# 0021. Build the GUI without secrets and keep the signing key on disk only while it signs

- **Status:** Proposed
- **Date:** 2026-11-19
- **Deciders:** Arkeep maintainers

## Context

The release job holds the registry credentials (ADR 0017) and, since the
agent packages, the package repository signing key (ADR 0020). It logged in
to GHCR and Docker Hub, which writes `~/.docker/config.json`, and wrote the
signing key to disk with its path in the job environment, all before running
GoReleaser. The first GoReleaser hook then ran `pnpm install` and `pnpm build`
for the GUI the server embeds.

The lockfile is frozen and pnpm does not run the install scripts of
dependencies, but building the GUI still runs Vite and its plugins. A
compromised dependency would run in the same job as the credentials and the
key and could read them. The key has no passphrase, is valid for three
years, and signs the metadata every machine using pkg.arkeep.io trusts: once
stolen, the only remedy is to rotate it on every machine.

## Decision

- **No npm code in a job that holds secrets.** The GUI is built in its own
  job of the release workflow, with no environment, no secret and a
  read-only token, and handed to the release job as an artifact of the same
  run. The release job does not install Node.js or pnpm.
- **GoReleaser never builds the GUI.** Its hooks only check that
  `gui/dist/index.html` exists and copy it into the server. Whoever runs
  GoReleaser builds the GUI first: the `gui` job in the release workflow,
  a step of its own in `packages.yml`, `task build:gui` locally.
- **The signing key is on disk only while it signs.** In the release job it
  is written just before GoReleaser, which alone gets its path to sign the
  `.rpm` packages, and removed right after, even when GoReleaser fails. The
  repository publication, in the release workflow and in the `Agent
  repository` workflow, writes it after installing its tools and downloading
  the releases, and removes it as soon as `scripts/agent-repository.sh build`
  has signed, before the upload.
- **No cache in the release and `Agent repository` workflows.** `setup-go`
  and `setup-node` run with the cache off there: a cache written by another
  run must not reach the release, its key or the embedded GUI. CI and
  `packages.yml`, which publish nothing, keep theirs.

## Consequences

- A compromised npm dependency can still alter the GUI it builds, which ships
  in the server binary, but it cannot read the registry credentials, the
  signing key or a token that writes to the repository.
- While the key is on disk, the release job runs only GoReleaser (Go builds,
  `download_deps.go`, image builds in containers that do not see the
  runner's files, nfpm) and the repository script.
- A release takes one more job; the GUI artifact is kept one day.
- A local `goreleaser release --snapshot` needs `task build:gui` first, and
  stops with a message if the GUI is missing. A stale `gui/dist` is embedded
  as it is.
- A new step that runs third-party build code goes in a job without
  secrets, never in the release job.

## Alternatives considered

- **Keep the hook and skip it in the release job with a variable** — fewer
  changes, but the protection would rest on a variable, and the hook would
  still be one setting away from running in the release job.
- **Build the GUI in the release job before the secrets are written** —
  the registry logins and the key could be moved after it, but code that
  runs during the build can leave a process or a modified file behind for
  the later steps.
- **A separate job for signing** — the key would never share a job with
  GoReleaser, but the `.rpm` packages are signed by nfpm inside GoReleaser,
  so it would mean re-signing them after the build.

## References

- `.github/workflows/release.yml` (jobs `gui` and `release`),
  `.github/workflows/agent-repository.yml`, `.github/workflows/packages.yml`.
- `.goreleaser.yml` (`before.hooks`), `Taskfile.yml` (`build:gui`).
- ADR 0013, ADR 0017, ADR 0020.
