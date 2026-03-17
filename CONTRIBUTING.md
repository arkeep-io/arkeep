# Contributing to Arkeep

Thank you for your interest in contributing to Arkeep.
This document covers everything you need to get your changes merged.

## Table of Contents

- [Before You Start](#before-you-start)
- [Development Setup](#development-setup)
- [Project Conventions](#project-conventions)
- [Extension Points](#extension-points)
  - [Adding a New Destination Type](#adding-a-new-destination-type)
  - [Adding a New Source Type](#adding-a-new-source-type)
- [Testing](#testing)
- [Pull Request Process](#pull-request-process)
- [Security Vulnerabilities](#security-vulnerabilities)
- [License](#license)

---

## Before You Start

- Check the [open issues](https://github.com/arkeep-io/arkeep/issues) to see if your
  idea or bug is already being tracked.
- For non-trivial changes, open an issue first to discuss the approach before writing code.
  This avoids wasted effort if the direction doesn't fit the project.

---

## Development Setup

See the [Development section in README.md](README.md#development) for the full setup guide.
The short version:

```bash
git clone https://github.com/arkeep-io/arkeep
cd arkeep
task deps:download   # download restic + rclone binaries
task proto           # generate gRPC code
task run:server      # terminal 1
task run:gui         # terminal 2
task run:agent       # terminal 3
```

---

## Project Conventions

### Go

- Follow standard Go conventions: `gofmt`, idiomatic error handling, small interfaces.
- All comments and documentation must be written in **English**.
- Error strings must be lowercase and must not end with punctuation (`fmt.Errorf("something failed: %w", err)`, not `"Something failed."`).
- Sentinel errors are defined per package (`var ErrNotFound = errors.New(...)`).
- Use `uuid.NewV7()` (time-ordered) for all new ID generation — never `uuid.New()`.
- **Do not use `gorm.Preload`** — all relations are loaded with explicit queries in the repository layer.
- **Do not add GORM soft delete** to new models — only `agents` and `policies` use it.
- The `EncryptedString` type is cast with `db.EncryptedString(value)`, not `.String()`.
- Table-driven tests with the standard `testing` package — no external test frameworks.
- Run `task lint` before submitting. All `golangci-lint` checks must pass.

### Vue / TypeScript

- Composition API with `<script setup>` only — no Options API.
- Type everything — no implicit `any`.
- Use `useField()` from vee-validate — never the `<Field>` component (conflicts with shadcn-vue).
- `Switch` from shadcn-vue emits `update:model-value`, not `update:checked`.
- Styling with Tailwind CSS v4 utility classes only — no custom `<style>` blocks.
- `api<T>()` is the HTTP client (`services/api.ts`) — use it for all API calls.
- Run `task lint` before submitting.

### Commits

Use [Conventional Commits](https://www.conventionalcommits.org/) format:

```
feat: add SFTP destination type
fix: handle restic init error on first backup
docs: update agent configuration reference
chore: bump golangci-lint to v2.11
refactor: extract destination credential validation
test: add unit tests for retention policy scheduler
```

---

## Extension Points

### Adding a New Destination Type

Destination types are defined in three places. Follow these steps to add a new one:

**1. Database — add the type string**

In `server/internal/db/migrations/`, add a new migration that updates the `CHECK` constraint
on `destinations.type` to include your new type string (e.g. `'azure'`).

**2. Server — credential handling**

In `server/internal/api/` (destinations handler), add a case for your new type to:
- `validateDestinationCredentials()` — validate required fields
- `buildDestinationEnv()` — map credentials to the environment variables Restic/Rclone expects

**3. Agent — Restic/Rclone mapping**

In `agent/internal/restic/wrapper.go`:
- Add a new `DestinationType` constant (e.g. `DestAzure DestinationType = "azure"`).
- Update `buildCmd()` to set the correct `RESTIC_REPOSITORY` format and any backend-specific
  environment variables for your destination type.

**4. GUI — destination form**

In `gui/src/components/destinations/DestinationSheet.vue`:
- Add your type to the type selector.
- Add a conditional form section (using `v-if="form.type === 'azure'"`) with the required credential fields.
- Add a matching entry to the `destinationTypeLabels` map.

**5. Tests**

Add at least one test in `agent/internal/restic/` that verifies the repository URL format
and environment variable mapping for your new type.

---

### Adding a New Source Type

Source types define what gets backed up. The current types are `directory` and `docker-volume`.

**1. Agent — executor**

In `agent/internal/executor/`, update `buildSourcesList()` to handle your new source type prefix
(e.g. `pg-database://`). This function translates the JSON sources array from the server into
the list of paths passed to `restic.Backup()`.

If your source type requires a pre-backup step (like `pg_dump`), implement it as a pre-backup
hook rather than a new source type — see `agent/internal/hooks/` for the hooks runner.

**2. Server — policy form**

In `gui/src/components/policies/PolicySheet.vue`:
- Add your type to the source type selector.
- Add the corresponding form fields and any validation logic.
- Update the JSON serialization in `buildSourcesJSON()` to produce the format the agent expects.

**3. Server — scheduler**

In `server/internal/scheduler/scheduler.go`, update `buildSourcesList()` if the server needs to
transform your source type before sending it to the agent via gRPC.

---

## Testing

```bash
task test          # run all tests
task test:server   # run server tests only
task test:agent    # run agent tests only
task test:gui      # run GUI tests only
```

- **Go:** table-driven tests with the standard `testing` package. Place test files next to the
  code they test (`foo_test.go` alongside `foo.go`).
- **GUI:** Vitest for unit tests. Place test files in `__tests__/` next to the component or
  composable being tested.
- Tests must pass on CI before a PR can be merged. The CI matrix runs tests on `ubuntu-latest`.

---

## Pull Request Process

1. Fork the repository and create a branch from `main`:
   ```bash
   git checkout -b feat/my-feature
   ```
2. Make your changes. Write or update tests where appropriate.
3. Run `task test` and `task lint` — both must pass locally before opening a PR.
4. Open a pull request against `main` with a clear description of **what** changed and **why**.
5. Fill in the PR template. Incomplete PRs may be closed without review.
6. A maintainer will review your PR. Address feedback and push additional commits to the same branch —
   do not force-push after review has started.
7. Once approved, your PR will be squash-merged.

---

## Security Vulnerabilities

**Do not open a public issue for security vulnerabilities.**

Please report security issues by email — see [SECURITY.md](SECURITY.md) for details.

---

## License

By contributing to Arkeep you agree that your contributions will be licensed under the
[Apache License 2.0](LICENSE) that covers the project.