# Architecture Decision Records

This directory records the architecture decisions that shape Arkeep: what was
decided, why, and what it implies. An ADR is short, written once, and never
rewritten afterwards. When a decision changes, a new ADR supersedes the old
one, so the history stays readable.

## When to write an ADR

Write one when a change:

- introduces or replaces a component, protocol, storage engine or dependency
  that is hard to remove later;
- sets a rule contributors must follow (security model, data layout, release
  process, coding constraint that is not obvious from the code);
- changes or reverses a previous ADR.

Bug fixes and features that follow existing decisions do not need one.

## How to write an ADR

No tooling is required.

1. Copy [`template.md`](template.md) to `NNNN-short-title.md`, using the next
   free number (four digits) and a lowercase, hyphenated title.
2. Fill it in with the status **Proposed** and open a pull request. The
   discussion happens on the pull request.
3. Before merging, set the status to **Accepted** and the date to the merge
   date, and add the ADR to the index below.
4. To replace a decision, write a new ADR and change the old one's status to
   **Superseded by [NNNN](NNNN-title.md)**. Apart from this status line, an
   accepted ADR is not edited.

Statuses: **Proposed** (under discussion), **Accepted** (in force),
**Deprecated** (no longer applies, nothing replaces it), **Superseded**
(replaced by a newer ADR).

## Index

ADRs 0002 to 0011 document decisions taken before this directory existed. They
describe the architecture as implemented and are marked **Accepted**.

| ADR | Title | Status |
|---|---|---|
| [0001](0001-record-architecture-decisions.md) | Record architecture decisions | Accepted |
| [0002](0002-server-agent-architecture-with-agent-initiated-grpc.md) | Server/agent architecture with agent-initiated gRPC | Accepted |
| [0003](0003-restic-and-rclone-embedded-in-the-agent.md) | Use restic and rclone, embedded in the agent | Accepted |
| [0004](0004-single-server-binary-with-embedded-gui-and-migrations.md) | Ship a single server binary with the GUI and migrations embedded | Accepted |
| [0005](0005-sqlite-by-default-postgresql-supported.md) | SQLite by default, PostgreSQL supported, explicit data access | Accepted |
| [0006](0006-auto-pki-and-per-agent-mtls.md) | Auto-PKI and per-agent mTLS identity | Superseded by [0023](0023-auto-pki-per-agent-mtls-and-enrollment-authenticated-with-the-agent-secret.md) |
| [0007](0007-encrypt-credentials-at-rest.md) | Encrypt credentials at rest, never return them | Accepted |
| [0008](0008-short-lived-jwt-and-refresh-cookie-sessions.md) | Short-lived JWT access tokens with refresh cookies | Accepted |
| [0009](0009-hooks-and-command-sources-run-on-the-agent-admin-only.md) | Hooks and command sources run as shell commands on the agent, admin only | Accepted |
| [0010](0010-per-destination-locks-and-fifo-queue.md) | Per-destination locks and a FIFO waiting queue | Accepted |
| [0011](0011-go-monorepo-conventional-commits-goreleaser.md) | Go monorepo, Conventional Commits and GoReleaser releases | Accepted |
| [0012](0012-oidc-group-based-access-and-absolute-session-lifetime.md) | Restrict OIDC sign-in by group and cap OIDC session lifetime | Accepted |
| [0013](0013-pin-and-verify-every-dependency.md) | Pin and verify every dependency | Accepted |
| [0014](0014-publish-the-helm-chart-as-an-oci-artifact.md) | Publish the Helm chart as an OCI artifact on GHCR and Docker Hub | Superseded by [0025](0025-publish-the-helm-chart-only-on-ghcr.md) |
| [0015](0015-bundle-postgresql-in-the-helm-chart-with-the-official-image.md) | Bundle PostgreSQL in the Helm chart with the official image | Accepted |
| [0016](0016-validate-release-tags-and-keep-latest-for-stable-releases.md) | Validate release tags and keep latest for stable releases | Accepted |
| [0017](0017-keep-registry-secrets-in-the-release-environment-and-attest-every-artifact.md) | Keep registry secrets in the release environment and attest every published artifact | Accepted |
| [0018](0018-publish-the-agent-as-a-home-assistant-app.md) | Publish the agent as a Home Assistant app from a branch of this repository | Superseded by [0024](0024-publish-the-home-assistant-app-with-a-deploy-key-and-images-only-the-release-can-write.md) |
| [0019](0019-send-notifications-to-push-and-chat-services-through-shoutrrr.md) | Send notifications to push and chat services through shoutrrr | Accepted |
| [0020](0020-publish-the-agent-packages-in-an-apt-and-rpm-repository-on-cloudflare-r2.md) | Publish the agent packages in an APT and RPM repository on Cloudflare R2 | Accepted |
| [0021](0021-build-the-gui-without-secrets-and-keep-the-signing-key-on-disk-only-while-it-signs.md) | Build the GUI without secrets and keep the signing key on disk only while it signs | Accepted |
| [0022](0022-rate-limit-ipv6-clients-per-64-and-count-second-factor-codes-per-account.md) | Rate limit IPv6 clients per /64 and count second-factor codes per account | Accepted |
| [0023](0023-auto-pki-per-agent-mtls-and-enrollment-authenticated-with-the-agent-secret.md) | Auto-PKI, per-agent mTLS, and enrollment authenticated with the agent secret | Accepted |
| [0024](0024-publish-the-home-assistant-app-with-a-deploy-key-and-images-only-the-release-can-write.md) | Publish the Home Assistant app with a deploy key, and images only the release can write | Accepted |
| [0025](0025-publish-the-helm-chart-only-on-ghcr.md) | Publish the Helm chart only on GHCR | Accepted |
| [0026](0026-repair-repositories-from-the-gui-under-a-destination-maintenance-mode.md) | Repair repositories from the GUI under a destination maintenance mode | Accepted |
| [0027](0027-read-only-api-tokens-for-programs-that-read-the-api.md) | Read-only API tokens for programs that read the API | Accepted |
| [0028](0028-refresh-gui-pages-from-change-events-published-by-the-repositories.md) | Refresh GUI pages from change events published by the repositories | Accepted |
