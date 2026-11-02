# 0014. Publish the Helm chart as an OCI artifact on GHCR and Docker Hub

- **Status:** Proposed
- **Date:** 2026-11-02
- **Deciders:** Arkeep maintainers

## Context

The Helm chart lives in `deploy/helm/arkeep` and was never published: users had
to clone the repository and run `helm dependency build` before installing.
`Chart.yaml` stayed at version `0.1.0` whatever the release, so a chart could
not be tied to the Arkeep version it deploys.

Helm charts are distributed in two ways:

- a classic chart repository: an `index.yaml` and the packaged charts served
  over HTTP. chart-releaser, the usual tool for GitHub projects, uploads the
  packages to GitHub Releases and serves `index.yaml` from a `gh-pages` branch
  through GitHub Pages;
- an OCI registry: since Helm 3.8 a chart is pushed with `helm push` to any
  container registry and installed with `helm install oci://…`, without
  `helm repo add`.

Arkeep already publishes its images to GHCR and Docker Hub from the release
workflow, logged in to both.

## Decision

- Every release (`v*` tag) publishes the chart as an OCI artifact to
  `oci://ghcr.io/arkeep-io/charts/arkeep` and
  `oci://registry-1.docker.io/arkeepio/arkeep`, from `release.yml`, after
  GoReleaser has published the images.
- The chart version follows the release: tag `v1.2.3` publishes chart `1.2.3`
  with `appVersion` `v1.2.3`, set by `helm package --version --app-version`.
  `version` in `Chart.yaml` is a placeholder for installs from a clone and is
  not bumped by hand. Pre-release tags publish pre-release charts, which
  `helm install` skips unless asked for with `--version` or `--devel`.
- Helm is installed in the workflow with `go install` at an exact version
  (ADR 0013). Dependencies are built from the committed `Chart.lock`.
- The chart is listed on Artifact Hub, which indexes the GHCR repository.

## Consequences

- Users install and upgrade with one command and no clone. The installation
  documentation and the release notes point to the OCI reference.
- The chart is released with the application. A chart-only fix needs an
  Arkeep release.
- A release whose chart push fails has images and binaries but no chart: the
  chart can be pushed again by hand from the tag.
- The GHCR package and the Docker Hub repository must be public. A package
  created by the first push on GHCR is private until its visibility is changed.
- `Chart.lock` must stay in sync with `Chart.yaml`: `helm dependency build`
  fails otherwise, in the release workflow and for users installing from a
  clone.

## Alternatives considered

- **chart-releaser with GitHub Pages** — needs a `gh-pages` branch and Pages
  only to serve `index.yaml`, a second publishing channel next to the
  registries already in use.
- **Installing from a clone only** — the situation this replaces.
- **An independent chart version** — semantically more precise, but a second
  version to bump by hand at every chart change.

## References

- `.github/workflows/release.yml`, `deploy/helm/arkeep/`.
- [Helm — Use OCI-based registries](https://helm.sh/docs/topics/registries/).
- ADR 0011 (releases), ADR 0013 (dependency pinning).
