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
- Read the [Architecture Decision Records](docs/adr/README.md): they explain the
  choices your change must respect. A change that introduces or reverses an
  architectural decision comes with a new ADR (copy [the template](docs/adr/template.md)).

---

## Development Setup

See [Development setup](https://arkeep.io/docs/contributing/development/) in the docs for the full setup guide.
The short version:

```bash
git clone https://github.com/arkeep-io/arkeep
cd arkeep
task deps:download   # download restic + rclone binaries
task proto           # generate gRPC code
task run:server      # terminal 1
task run:gui         # terminal 2
task run:agent       # terminal 3 — agent connecting to localhost:9090
```

To build a production binary with the GUI embedded:
```bash
task build:server   # builds GUI first, then compiles Go with GUI embedded
```

Toolchains and tools are pinned ([ADR 0013](docs/adr/0013-pin-and-verify-every-dependency.md)):
Go follows the `toolchain` in `go.work` (fetched automatically by `go`), Node follows `.nvmrc`, pnpm
follows `packageManager` in `gui/package.json`, and protoc must be the version used in CI (29.3). Task,
protoc-gen-go, protoc-gen-go-grpc, govulncheck and gosec are pinned in `tools/go.mod`: install them with
`cd tools && GOWORK=off go install tool`, then `task tools:install` keeps them in sync. Dependabot and a
weekly workflow propose updates; `task deps:update`, `task deps:pins:update` and `task deps:vulncheck` do
the same locally.

---

## Project Conventions

### Go

- Follow standard Go conventions: `gofmt`, idiomatic error handling, small interfaces.
- All comments and documentation must be written in **English**.
- Error strings must be lowercase and must not end with punctuation (`fmt.Errorf("something failed: %w", err)`, not `"Something failed."`).
- Sentinel errors are defined per package (`var ErrNotFound = errors.New(...)`).
- Use `uuid.NewV7()` (time-ordered) for all new ID generation — never `uuid.New()`.
- **Do not use `gorm.Preload`** — all relations are loaded with explicit queries in the repository layer.
- **Do not add GORM soft delete** to new models — only `agents`, `policies` and `destinations` use it.
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

#### Forms

- Every form is validated with vee-validate: `useForm({ validationSchema: toTypedSchema(schema), initialValues })`
  with a zod schema, one `useField()` per field, and `handleSubmit` on submit.
- Declare `initialValues` for every field, so an untouched field fails with its own message rather
  than zod's generic type error.
- Show a field's error under it with `FieldError` and a red border on the input. Keep the `Alert`
  at the top of the form for errors returned by the server.
- When a component holds several forms, bind each field to its form: `useField(name, undefined, { form })`.
- Shared rules live in `src/lib/validation.ts` and mirror a server rule — the server stays the
  authority, so change both together. Number inputs use `integerInRange(min, max)`: an empty field
  is an error, and `0` must be typed when it disables a feature.
- Every form has a component test (see [Testing](#testing)).

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

**1. Database — add the type string**

In `server/internal/db/migrations/`, add a migration that updates the `CHECK` constraint on
`destinations.type` to include your new type string (e.g. `'azure'`). Changing a `CHECK`
constraint needs separate files for SQLite and PostgreSQL (see the `sqlite/` and `postgres/`
folders).

**2. Server — repository URL, environment and validation**

In `server/internal/destutil/destutil.go`:
- `BuildRepoURL()` — build the restic repository URL from the destination config.
- `BuildEnv()` — map credentials to a **fixed** set of Restic/Rclone environment variables. Never
  pass user-supplied keys through: restic and rclone honour variables such as
  `RESTIC_PASSWORD_COMMAND` or `LD_PRELOAD` that run commands on the agent.

In `server/internal/destutil/validate.go`, add the required fields of the new type to
`ValidateConfig()` and `ValidateCredentials()`, mirroring the GUI schema: the server is the
authority, not the GUI. Then add the type to `validDestinationTypes` in
`server/internal/api/destinations.go`.

**3. Agent — environment allowlist**

Add every environment variable the new type needs to `allowedDestEnv` in
`agent/internal/restic/env.go`. The agent drops any other key before running restic.

**4. GUI — destination form**

In `gui/src/components/destinations/DestinationSheet.vue`:
- Add the type to the `DEST_TYPES` list (value and label for the type selector).
- Add a conditional form section with its credential fields.

**5. Tests**

Add tests in `server/internal/destutil/` (repository URL, environment variables, validation) and
in `agent/internal/restic/` (environment allowlist).

---

### Adding a New Source Type

Source types define what gets backed up. The current types are `directory`, `docker-volume` and
`command`.

**1. Server — flattening the sources**

In `server/internal/policyutil/sources.go`, update `SourcePaths()` (the paths passed to the
regular restic invocation) and/or add a dedicated extractor like `CommandSources()`, depending on
whether the type produces a path or something else entirely.

**2. Agent — executor**

In `agent/internal/executor/executor.go`, update `resolveSources()` for a path-shaped type
(prefix convention, e.g. `docker-volume://`), or add its own dispatch in `executeBackup()` for a
type that is not a filesystem path at all (like `command`).

**3. GUI — policy form**

In `gui/src/components/policies/PolicySheet.vue`:
- Add the type to `SOURCE_TYPES` and the type selector.
- Add the form fields and their validation to `sourceItemSchema`.
- The JSON serialization in `onSubmit` already passes `{type, path, label}` through for any type
  other than `docker-volume`; only special-case it if the new type needs it.

If the source needs a pre-backup step whose result *is* a path worth backing up (e.g. exporting to
a file first), a pre-backup hook (`agent/internal/hooks/`) is still the right tool. For data that
should stream into restic without touching disk (e.g. `pg_dump` through restic's
`--stdin-from-command`), use the `command` source type: see `CommandSources()`,
`BackupFromCommand()` in `agent/internal/restic/wrapper.go`, and the admin-only gating in
`server/internal/api/policies.go` (a command source runs a shell command on the agent, like a
hook).

---

## Testing

```bash
task test          # run all tests
task test:server   # run server tests only
task test:agent    # run agent tests only
task test:gui      # run GUI tests only
```

- **Go:** table-driven tests with the standard `testing` package. Place test files next to the
  code they test (`foo_test.go` alongside `foo.go`). Server tests get their database from
  `dbtest.New(t)` (`server/internal/db/dbtest`), a copy of a database migrated once per test
  binary: running the migrations for every test made the suite several times slower under the
  race detector. A package that hashes many passwords calls `auth.UseCheapPasswordHashing()`
  from its `TestMain`.
- **GUI:** Vitest with Vue Test Utils and jsdom. Place test files next to the code they test
  (`Foo.test.ts` alongside `Foo.vue`). Mount pages and components with `mountPage` from
  `src/test/mount.ts`, submit with `submitForm`, and replace the HTTP client with
  `vi.mock('@/services/api', …)` — see an existing form test. A `beforeEach` callback must not
  return a value: Vitest runs a returned function as teardown, so write
  `beforeEach(() => { vi.mocked(api).mockReset() })`, with braces.
- Tests must pass on CI before a PR can be merged. The CI matrix runs tests on `ubuntu-26.04`.

---

## Pull Request Process

The repository follows Gitflow. `develop` is the integration branch — all feature work and
external contributions flow here. `main` receives merges only from `release/*` and `hotfix/*`
branches, handled exclusively by the maintainer. External contributors always target `develop`,
never `main`.

1. Fork the repository and create a branch from `develop`:
   ```bash
   git checkout -b feat/my-feature
   ```
   Do not force-push to your branch after review has started — push additional commits instead.
2. Make your changes. Write or update tests where appropriate.
3. Run `task test` and `task lint` — both must pass locally before opening a PR.
4. Open a pull request against `develop` with a clear description of **what** changed and **why**.
5. Fill in the PR template. Incomplete PRs may be closed without review.
6. A maintainer will review your PR. Address feedback and push additional commits to the same branch.
7. Once approved, your PR will be squash-merged.

---

## Security Vulnerabilities

**Do not open a public issue for security vulnerabilities.**

Please report security issues by email — see [SECURITY.md](SECURITY.md) for details.

---

## License

By contributing to Arkeep you agree that your contributions will be licensed under the
[Apache License 2.0](LICENSE) that covers the project.