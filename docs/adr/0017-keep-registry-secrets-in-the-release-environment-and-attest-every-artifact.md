# 0017. Keep registry secrets in the release environment and attest every published artifact

- **Status:** Proposed
- **Date:** 2026-11-12
- **Deciders:** Arkeep maintainers

## Context

Releases are built by `.github/workflows/release.yml` from a validated `v*`
tag (ADR 0016), and only administrators can create those tags. Two gaps
remained:

- The Docker Hub credentials were repository secrets. Any workflow run on a
  branch pushed by someone with write access could read them and publish
  images without going through the release workflow or its tag checks.
- Releases carried only `checksums.txt`, published in the same release it is
  meant to verify. Whoever could publish, or took over a registry account,
  could replace binaries, images or the chart without users being able to
  tell. ADR 0013 covers what Arkeep consumes, not what it publishes.

Images, binaries and the chart run with high privileges: the agent runs as
root on every machine it backs up.

## Decision

- The release job runs in the `release` environment, which only `v*` tags
  can deploy to. Credentials for publishing (Docker Hub today) are
  environment secrets of `release`, never repository secrets.
- Every artifact the release publishes gets a build provenance attestation
  from the release workflow (`actions/attest-build-provenance`, signed with
  Sigstore, stored by GitHub): the archives, `checksums.txt`, the server and
  agent images and the Helm chart. Images pushed to several registries from
  one build share a digest and one attestation.
- An artifact added to the release (a new archive, image or chart) must be
  added to the "Collect the artifacts to attest" step in the same change.
- The release notes tell users to verify with
  `gh attestation verify … --repo arkeep-io/arkeep --signer-workflow
  arkeep-io/arkeep/.github/workflows/release.yml`. Renaming or moving the
  workflow, or removing the attestation, breaks every user's verification
  and needs a new ADR.

## Consequences

- A workflow on any branch other than a `v*` tag cannot read the Docker Hub
  credentials. The environment and its secrets are GitHub settings, outside
  the repository: they are set up once by an administrator.
- Users can check that a download, image or chart was built by the release
  workflow of this repository from a given commit, without keys to manage.
- GHCR is still written with `GITHUB_TOKEN`, which a workflow on any branch
  can request with `packages: write`. Such an image has no attestation from
  the release workflow, so the documented verification rejects it.
- Older releases (up to v0.10.0) have no attestation.
- The attestation action is pinned by commit SHA like every other action
  (ADR 0013).

## Alternatives considered

- **Required reviewer on the environment** — one more approval per release;
  not needed while a single maintainer can create release tags.
- **cosign signatures and an SBOM** — broader coverage, but two more tools to
  pin and run in every release, with no current consumer asking for them.
- **Checksums only** — they detect corruption, not a replaced release.

## References

- `.github/workflows/release.yml` (job `environment`, steps "Collect the
  artifacts to attest" and "Attest build provenance").
- `.goreleaser.yml` (release notes footer, "Verify").
- ADR 0011, ADR 0013, ADR 0016.
- [Using artifact attestations](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations/using-artifact-attestations-to-establish-provenance-for-builds)
