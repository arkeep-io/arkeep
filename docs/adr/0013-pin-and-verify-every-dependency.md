# 0013. Pin and verify every dependency

- **Status:** Accepted
- **Date:** 2026-10-29
- **Deciders:** Arkeep maintainers

## Context

Arkeep runs with high privileges: agents read every file they back up, often
as root, and run restic and rclone binaries embedded at build time
([0003](0003-restic-and-rclone-embedded-in-the-agent.md)). The server holds
every repository credential ([0007](0007-encrypt-credentials-at-rest.md)). A
compromised dependency, build tool or base image would reach every user's
infrastructure through a regular release.

Supply-chain attacks of this kind are common: moved Git tags on popular GitHub
Actions (`tj-actions/changed-files`, 2025), self-propagating malicious npm
releases (the "Shai-Hulud" worm, 2025), and hijacked or typosquatted packages
and images. They share a pattern: the victim resolves a **mutable reference**
(`latest`, a tag, a version range) at build time and gets content it never
reviewed.

Arkeep's builds mix several ecosystems: Go modules, npm packages, GitHub
Actions, container images, Helm charts and binaries downloaded from upstream
releases. Today they are pinned unevenly:

- Go modules and npm packages are locked (`go.sum`, `pnpm-lock.yaml`), but
  `gui/package.json` declares ranges (`^x.y.z`) and `packageManager` has no
  hash.
- CI installs Go and Node by major or minor version (`"1.27"`, `"26"`), and
  some tools as `latest` (`protoc-gen-go@latest`, golangci-lint
  `version: latest`).
- Actions are referenced by tag (`actions/checkout@v7`).
- Base images are referenced by tag (`golang:1.27-alpine`, `alpine:3.24`),
  deployment examples use `:latest` for Arkeep's images, and the Helm chart
  depends on PostgreSQL `16.x.x`.
- restic and rclone are downloaded at build time
  ([0003](0003-restic-and-rclone-embedded-in-the-agent.md)) without any
  integrity check.
- Dependabot covers Go modules, npm, Dockerfiles and actions, with no
  cooldown, and does not cover compose files or the Helm chart.
- Security CI runs `gosec` and a weekly Trivy image scan, but nothing checks
  Go or npm dependencies for known vulnerabilities.

## Decision

**Every dependency used to develop, build, test, release or run Arkeep is
referenced by an exact, immutable version, verified by a cryptographic hash
wherever the ecosystem allows it.** Resolution happens when a dependency is
added or updated, in a reviewed pull request, never implicitly at build time.

### Rules per ecosystem

| Ecosystem | Pin | Integrity |
|---|---|---|
| Go modules | exact versions in `go.mod` | `go.sum` committed, checked against the Go checksum database |
| Go toolchain | `toolchain goX.Y.Z` in `go.mod` / `go.work`; CI reads it (`go-version-file`) | Go toolchain download is checksum-verified by `go` |
| Go-based tools (protoc plugins, linters, Task, scanners…) | exact version, declared as `tool` dependencies in `tools/go.mod` or installed with `go install pkg@vX.Y.Z` | `go.sum` / checksum database |
| npm packages | **exact** versions in `package.json` (no `^`, `~`, `*`, `latest`) | `pnpm-lock.yaml` committed with integrity hashes; installs use `--frozen-lockfile` |
| pnpm, Node.js | `packageManager` with its `+sha512` hash; Node version in `.nvmrc`, read by CI (`node-version-file`) | hash in `packageManager`; Node image pinned by digest (see below) |
| GitHub Actions | **full commit SHA**, with the version as a comment (`uses: org/action@<sha> # v1.2.3`) | the SHA is the content address |
| Container images (Dockerfiles, compose, Helm defaults, CI) | `name:exact-tag@sha256:<digest>` | the digest is the content address |
| Arkeep's own images in deployment examples | the current release tag (`:vX.Y.Z`), updated at each release | — |
| Helm chart dependencies | exact version in `Chart.yaml` | `Chart.lock` committed, with its digest |
| Binaries downloaded from upstream (restic, rclone, protoc…) | exact version in the repository | **SHA-256 pinned in the repository**, verified before use. When the pin changes, the upstream signature (GPG for restic and rclone) is verified. If upstream publishes none (protoc), the hash is taken from the official release and the pull request says so |

### What is forbidden

- `latest`, or any floating reference, for anything Arkeep consumes: image
  tags, tool versions (`@latest`, `version: latest`), Node or Go versions given
  as a major or minor only (`"26"`, `"1.27"`).
- Version ranges in manifests (`^1.2.0`, `~1.2`, `16.x.x`, `>=`).
- Actions referenced by tag or branch.
- Downloading and executing anything (`curl | sh`, `npm install -g x`, an
  unverified binary) without a pinned version and hash.

Arkeep keeps **publishing** a `latest` tag of its own images for users who
want it. This ADR governs what Arkeep **consumes**.

### Accepted exceptions

- **OS packages** installed with `apk` or `apt` in Dockerfiles and CI runners
  are not pinned individually: distribution repositories drop old versions,
  which would break builds. They are installed from the distribution's signed
  repositories, on top of a base image pinned by digest, and the resulting
  images are scanned by Trivy (see Enforcement).
- **GitHub-hosted runner images** (`ubuntu-latest`) are managed by GitHub; the
  workflow steps that matter are pinned as described above.
- A pseudo-version (`v0.0.0-<date>-<commit>`) is allowed for a Go dependency
  when a fix only exists on an untagged commit. The reason and the condition to
  move back to a tag are written next to it.

Any other exception is marked at the point of use with a
`# pin-exempt: <reason>` comment, which the CI check recognizes and reviewers
review like code.

### Keeping pins up to date

Pinning must not mean freezing. Updates are automated and reviewed:

- **Dependabot** proposes updates for every supported ecosystem (Go modules,
  npm, GitHub Actions SHAs, Docker digests in Dockerfiles and compose files,
  Helm). Updates are grouped per ecosystem to keep the number of pull requests
  low.
- A **cooldown of 7 days** applies before a new release is proposed, so that
  hijacked releases are usually detected and yanked first. **Security updates
  are not delayed.**
- restic and rclone, which Dependabot cannot track, are updated by a
  **scheduled workflow** that opens a pull request with the new version and
  its GPG-verified hashes. The same workflow keeps the Helm chart's
  `appVersion` on the latest release.
- The few remaining pins that no tool tracks (Go and Node toolchain versions,
  protoc) are updated by hand. The CI check keeps them exact.
- All updates go through CI like any other pull request.

### Enforcement

- A CI check (`scripts/check-pins.sh`) runs on every pull request and fails
  when a forbidden reference appears: floating tag, unpinned action, version
  range, `@latest`, or image without a digest outside documented exceptions.
- Known vulnerabilities are checked on every pull request and weekly with
  `govulncheck` for Go and `pnpm audit` for npm. Trivy scans the built images
  weekly and on every push to `main`. It is not run on pull requests because
  it builds both images. High and critical findings fail the check.

## Consequences

- Builds are reproducible: the same commit resolves the same dependencies
  today and in a year.
- A compromised upstream release, moved tag or tampered download cannot enter
  a build without changing a hash in a reviewed pull request.
- The number of dependency pull requests grows. Grouping, the cooldown and the
  scheduled workflow keep it manageable, and each one is small and
  mechanical.
- Pins must be updated deliberately. Without the automation above, they would
  age and accumulate vulnerabilities, so the automation is part of this
  decision, not optional.
- Deployment examples change at each release (image tags), which the release
  process must handle.

## Alternatives considered

- **Lockfiles only, ranges in manifests** — covers Go and npm but not actions,
  images, tools or downloaded binaries, which are the weakest links.
- **Pin versions without hashes** — a version tag can be moved or re-published;
  only a content hash proves the content is the one that was reviewed.
- **Vendoring every dependency** — maximal control, but a very large repository
  and a heavy update process for a small team.
- **Renovate instead of Dependabot** — more flexible (custom managers for
  restic/rclone), but an extra app to install and configure. Can be
  reconsidered if Dependabot's coverage proves insufficient.

## References

- [OpenSSF Scorecard — Pinned-Dependencies](https://github.com/ossf/scorecard/blob/main/docs/checks.md#pinned-dependencies)
- [SLSA](https://slsa.dev/) build requirements.
- [GitHub — Security hardening for GitHub Actions](https://docs.github.com/en/actions/security-for-github-actions/security-guides/security-hardening-for-github-actions#using-third-party-actions)
- [Dependabot cooldown](https://docs.github.com/en/code-security/dependabot/working-with-dependabot/dependabot-options-reference#cooldown-)
