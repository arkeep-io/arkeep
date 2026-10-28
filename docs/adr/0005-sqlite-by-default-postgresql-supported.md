# 0005. SQLite by default, PostgreSQL supported, explicit data access

- **Status:** Accepted
- **Date:** 2026-10-12 (retro-documented; the decision predates this record)
- **Deciders:** Arkeep maintainers

## Context

Most installations are a single server with a handful of agents and need no
database to administer. Larger installations need more write concurrency, or
want a database they already operate (a managed PostgreSQL service, for
example). The server must also build without CGO
([0004](0004-single-server-binary-with-embedded-gui-and-migrations.md)).

Whatever the database, Arkeep runs as a **single server instance**: the
registry of connected agents and the snapshot download tickets live in the
server's memory, so several replicas would not share them.

## Decision

- **SQLite is the default** database, through the pure-Go `modernc.org/sqlite`
  driver (no CGO). **PostgreSQL is supported** for higher concurrency or to
  use a managed database. It does not make running several server instances
  possible.
- Data access goes through **GORM inside a repository layer**
  (`server/internal/repositories`), with these rules:
  - relations are loaded with explicit queries — **no `gorm.Preload`**;
  - **no GORM soft delete** on new models (only `agents`, `policies` and
    `destinations` use it);
  - new identifiers are **time-ordered UUIDv7** (`uuid.NewV7()`).
- The schema is managed by **SQL migrations** (golang-migrate). Shared
  migrations live in `migrations/`; dialect-specific ones live in
  `migrations/sqlite/` and `migrations/postgres/` under the same number.

## Consequences

- Zero-configuration start with a single file, and a path to PostgreSQL when
  needed.
- The server cannot be scaled horizontally. High availability means restarting
  the single instance quickly, not adding replicas.
- Every schema change must work on both databases, which sometimes means two
  migration files.
- Explicit queries keep the SQL predictable and reviewable, at the cost of more
  repository code.

## Alternatives considered

- **PostgreSQL only** — an extra service to run for most users.
- **`mattn/go-sqlite3`** — requires CGO and breaks static cross-compilation.
- **GORM AutoMigrate** — implicit, non-reviewable schema changes.

## References

- [FAQ](https://arkeep.io/docs/reference/faq/) ("SQLite or PostgreSQL?") and
  [Kubernetes](https://arkeep.io/docs/installation/kubernetes/) on arkeep.io/docs.
- `CONTRIBUTING.md` → Project Conventions / Go.
- `server/internal/db/`, `server/internal/repositories/`.
