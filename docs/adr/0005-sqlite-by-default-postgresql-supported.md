# 0005. SQLite by default, PostgreSQL supported, explicit data access

- **Status:** Accepted
- **Date:** 2026-10-12 (retro-documented; the decision predates this record)
- **Deciders:** Arkeep maintainers

## Context

Most installations are a single server with a handful of agents and need no
database to administer. Larger installations need concurrency and possibly
several server replicas. The server must also build without CGO
([0004](0004-single-server-binary-with-embedded-gui-and-migrations.md)).

## Decision

- **SQLite is the default** database, through the pure-Go `modernc.org/sqlite`
  driver (no CGO). **PostgreSQL is supported** for high concurrency or
  multiple replicas.
- Data access goes through **GORM inside a repository layer**
  (`server/internal/repositories`), with these rules:
  - relations are loaded with explicit queries — **no `gorm.Preload`**;
  - **no GORM soft delete** on new models (only `agents` and `policies` use it);
  - new identifiers are **time-ordered UUIDv7** (`uuid.NewV7()`).
- The schema is managed by **SQL migrations** (golang-migrate). Shared
  migrations live in `migrations/`; dialect-specific ones live in
  `migrations/sqlite/` and `migrations/postgres/` under the same number.

## Consequences

- Zero-configuration start with a single file, and a path to PostgreSQL when
  needed.
- Every schema change must work on both databases, which sometimes means two
  migration files.
- Explicit queries keep the SQL predictable and reviewable, at the cost of more
  repository code.

## Alternatives considered

- **PostgreSQL only** — an extra service to run for most users.
- **`mattn/go-sqlite3`** — requires CGO and breaks static cross-compilation.
- **GORM AutoMigrate** — implicit, non-reviewable schema changes.

## References

- `README.md` → FAQ "SQLite or PostgreSQL?".
- `CONTRIBUTING.md` → Project Conventions / Go.
- `server/internal/db/`, `server/internal/repositories/`.
