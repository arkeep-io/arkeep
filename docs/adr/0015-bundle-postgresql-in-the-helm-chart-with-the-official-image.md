# 0015. Bundle PostgreSQL in the Helm chart with the official image

- **Status:** Accepted
- **Date:** 2026-11-03
- **Deciders:** Arkeep maintainers

## Context

With `postgresql.enabled=true` the Helm chart deployed PostgreSQL through the
Bitnami `postgresql` subchart (chart 16.7.27, PostgreSQL 17.6). Since August
2025 Bitnami no longer publishes versioned images on `docker.io/bitnami`: the
subchart's default image `bitnami/postgresql:17.6.0-debian-12-r4` returns 404,
so a new install with the bundled database stays in `ImagePullBackOff`. The
old tags survive only on `docker.io/bitnamilegacy`, which receives no security
updates.

Newer Bitnami charts do not fix this. Chart 18.12.15, proposed by Dependabot,
defaults to `bitnami/postgresql:latest`, which ADR 0013 forbids, moves to
PostgreSQL 18, which cannot start on a data directory created by 17, and is not
served by `https://charts.bitnami.com/bitnami` anymore (403), the repository
the release workflow builds dependencies from.

The rest of the project runs PostgreSQL 17 from the official image: the compose
examples pin `postgres:17.11-alpine3.24` by digest. Arkeep needs one database,
one user and one replica (ADR 0005). It does not use replication, metrics or the
other features of the Bitnami chart.

## Decision

- The chart no longer depends on the Bitnami subchart. With
  `postgresql.enabled=true` it renders its own single-replica StatefulSet, a
  ClusterIP Service and a Secret, named `<fullname>-postgresql`.
- The image is the official `postgres` image, the same as in the compose
  examples, pinned as `tag@sha256:digest` in `values.yaml` (`postgresql.image`)
  and updated by Dependabot's `helm` ecosystem (ADR 0013). The compose files and
  the chart move together.
- The PostgreSQL major version only changes in a release that documents the
  dump and restore it needs. Major updates proposed by Dependabot are not
  merged as such.
- Data lives in `PGDATA=/var/lib/postgresql/data/pgdata` on a PVC created by the
  StatefulSet (`pgdata-<fullname>-postgresql-0`). The claim name differs from
  the Bitnami one (`data-…`) so that an upgrade can never start the new server
  on top of an old Bitnami data directory.
- The container runs as the image's `postgres` user (uid/gid 70), non-root,
  without privilege escalation and with all capabilities dropped.

## Consequences

- The bundled database installs again, receives the official image's security
  updates and matches what compose users run.
- The chart has no dependencies: `Chart.lock` is removed, and the release
  workflow no longer adds the Bitnami repository.
- Breaking change for installs that used the Bitnami subchart: the values move
  from `postgresql.primary.persistence.*` to `postgresql.persistence.*`, and the
  data must be moved with `pg_dump` and `pg_restore`. The procedure is in the
  Kubernetes installation guide. The old claim is left in place, so a rollback
  to the previous chart finds its data.
- The chart now owns a small StatefulSet that the maintainers must keep working
  (probes, security context, storage). Production setups that need high
  availability, point-in-time recovery or managed backups should keep using an
  external PostgreSQL (`externalPostgresql`).
- `scripts/check-pins.sh` also checks that the image references in the chart's
  `values.yaml` carry a digest.

## Alternatives considered

- **Pin the subchart's image to `bitnamilegacy/postgresql`** — works today, but
  the image is frozen without security updates and needs
  `global.security.allowInsecureImages`.
- **Upgrade to the latest Bitnami chart** — floating `latest` image, PostgreSQL
  18 and a chart repository that no longer serves it.
- **Drop the bundled database and require an external PostgreSQL** — simpler
  for the chart, but removes the one-command install with PostgreSQL that the
  compose examples offer.
- **Another PostgreSQL chart or operator (CloudNativePG)** — far more than one
  database for one replica needs, and an operator is a cluster-wide dependency.

## References

- `deploy/helm/arkeep/templates/postgresql-*.yaml`, `deploy/helm/arkeep/values.yaml`.
- `deploy/docker/docker-compose.postgres.yml` (same image).
- ADR 0005 (database), ADR 0013 (dependency pinning), ADR 0014 (chart publishing).
- [Bitnami: changes to the container catalog](https://github.com/bitnami/containers/issues/83267).

## Amendments

- 2026-11-03: the image is not updated by Dependabot's `helm` ecosystem, as the
  Decision says: its updater skips `image:` strings pinned by digest.
  Dependabot updates the image in `docker-compose.postgres.yml`, and the
  `deps-pins` workflow copies it to `postgresql.image` (and to the commented
  example in `docker-compose.yml`), so the compose files and the chart still
  move together.
