# 0016. Validate release tags and keep latest for stable releases

- **Status:** Accepted
- **Date:** 2026-11-05
- **Deciders:** Arkeep maintainers

## Context

Releases are cut by pushing a `v*` tag (ADR 0011). The release workflow
published whatever the tag pointed to: a stable version could be built from a
commit that never reached `main`, a malformed tag could produce a release, and
every release, pre-releases included, moved the `latest` Docker image. Users
following `latest` could end up on a beta.

The GitHub settings already protect `v*` tags against updates and deletion,
reserve their creation to administrators and make releases immutable. They do
not check where a tag points.

## Decision

- Release tags have one of three forms, with no leading zeros:
  `vX.Y.Z` (stable), `vX.Y.Z-beta.N` and `vX.Y.Z-rc.N` (pre-releases). Any other
  `v*` tag fails the release workflow.
- A stable tag must point to a commit in the history of `main`. A beta or rc
  tag must point to a commit in the history of `main` or `develop`. An older
  commit of those branches is allowed.
- The check runs as the first step after the checkout in
  `.github/workflows/release.yml`, before any build or registry login. It
  peels the tag to its commit, so lightweight and annotated tags are treated
  alike, and it requires the checked-out commit to be that commit.
- The `latest` Docker image tag moves only on stable releases. Pre-releases
  are published under their version tag only.

## Consequences

- A stable release requires the usual Gitflow path: `release/*` or `hotfix/*`
  merged into `main`, then tagged there.
- Beta and rc builds can still be cut from `develop` for testing.
- The latest Docker image tag is updated only by stable releases.
- Tags such as `v1.0.0-alpha.1` are no longer released. Adding a new
  pre-release kind requires a new ADR and a change to the workflow check.
- The check is shell code in the workflow, reviewed like the rest of the
  release pipeline (ADR 0011).

## Alternatives considered

- **Rely on the GitHub tag protections only** — they restrict who can tag,
  not what is tagged.
- **Allow any SemVer pre-release identifier** — more forms to reason about,
  with no current need for them.
- **Keep moving `latest` on pre-releases** — users on `latest` would run
  untested builds.

## References

- `.github/workflows/release.yml` (step "Validate release tag and source commit").
- `.goreleaser.yml` (`dockers_v2` tags).
- ADR 0011.
