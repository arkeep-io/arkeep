# 0025. Publish the Helm chart only on GHCR

- **Status:** Accepted
- **Date:** 2026-11-19
- **Deciders:** Arkeep maintainers

## Context

ADR 0014 publishes the Helm chart as an OCI artifact on two registries:
`oci://ghcr.io/arkeep-io/charts/arkeep` and
`oci://registry-1.docker.io/arkeepio/arkeep`. Artifact Hub lists the chart
from the GHCR repository only.

The Docker Hub copy has drawbacks and little use:

- On Docker Hub the chart sits next to the `arkeepio/arkeep-server` and
  `arkeepio/arkeep-agent` images under the name `arkeepio/arkeep`, which reads
  as a third image rather than a chart.
- It had 69 pulls by release v0.12.0. Artifact Hub, the installation
  documentation and the release notes all lead to GHCR first.
- It costs a registry login and a push in the release job, and a repository
  description kept in sync by the registry metadata workflow.

## Decision

- Every release publishes the chart only to
  `oci://ghcr.io/arkeep-io/charts/arkeep`. Everything else in ADR 0014 is
  unchanged: chart version and `appVersion` follow the tag, Helm is pinned,
  dependencies come from `Chart.lock`, and Artifact Hub lists the GHCR
  repository.
- The release workflow no longer logs in to Docker Hub to push the chart, and
  the registry metadata workflow no longer writes the description of the
  `arkeepio/arkeep` repository.
- The `arkeepio/arkeep` repository on Docker Hub is not deleted: the versions
  already published stay available, and its description points to GHCR. It
  is maintained by hand.

## Consequences

- One registry and one reference for the chart, the one Artifact Hub shows.
- Users who installed from Docker Hub get no new chart versions there; they
  move to the GHCR reference with `helm upgrade arkeep
  oci://ghcr.io/arkeep-io/charts/arkeep`. The release notes of the first
  release without the Docker Hub chart say so.
- The images are still published on Docker Hub: the Docker Hub token stays
  in the `release` environment (ADR 0017).
- The provenance attestation already covered the chart through its GHCR
  digest only; it is unchanged.

## Alternatives considered

- **Keep both registries (ADR 0014)** — a confusing name and an extra push
  for a copy almost nobody uses.
- **Rename the Docker Hub repository** (for example `arkeepio/arkeep-chart`)
  — fixes the name but keeps a second channel to maintain, and breaks the
  existing reference just the same.
- **Delete the Docker Hub repository** — breaks installs pinned to an
  existing version, for no gain over leaving it frozen.

## References

- ADR 0014 (Helm chart as an OCI artifact), ADR 0017 (registry secrets and
  attestation).
- `.github/workflows/release.yml` (step "Publish the Helm chart"),
  `.github/workflows/registry-metadata.yml`, `deploy/helm/arkeep/README.md`.
