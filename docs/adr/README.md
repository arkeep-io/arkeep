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
| [0006](0006-auto-pki-and-per-agent-mtls.md) | Auto-PKI and per-agent mTLS identity | Accepted |
| [0007](0007-encrypt-credentials-at-rest.md) | Encrypt credentials at rest, never return them | Accepted |
| [0008](0008-short-lived-jwt-and-refresh-cookie-sessions.md) | Short-lived JWT access tokens with refresh cookies | Accepted |
| [0009](0009-hooks-and-command-sources-run-on-the-agent-admin-only.md) | Hooks and command sources run as shell commands on the agent, admin only | Accepted |
| [0010](0010-per-destination-locks-and-fifo-queue.md) | Per-destination locks and a FIFO waiting queue | Accepted |
| [0011](0011-go-monorepo-conventional-commits-goreleaser.md) | Go monorepo, Conventional Commits and GoReleaser releases | Accepted |
| [0012](0012-oidc-group-based-access-and-absolute-session-lifetime.md) | Restrict OIDC sign-in by group and cap OIDC session lifetime | Proposed |
| [0013](0013-pin-and-verify-every-dependency.md) | Pin and verify every dependency | Proposed |
