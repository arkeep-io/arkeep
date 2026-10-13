# 0004. Ship a single server binary with the GUI and migrations embedded

- **Status:** Accepted
- **Date:** 2026-10-12 (retro-documented; the decision predates this record)
- **Deciders:** Arkeep maintainers

## Context

Arkeep targets self-hosters and small teams. Every extra moving part (a
separate web server for the GUI, a migration step to run by hand, a runtime to
install) makes installation and upgrades harder and more error-prone.

## Decision

- The GUI is a **Vue 3 single-page application, installable as a PWA**, built
  with Vite and **embedded in the server binary** (`go:embed`). The server
  serves it on the same port as the API, with a catch-all route for client-side
  routing.
- Database **migrations are embedded** in the server binary and applied
  automatically at startup with golang-migrate.
- The server is compiled with `CGO_ENABLED=0` into a static binary, also
  shipped as a minimal container image.

## Consequences

- Deployment is one binary or one image, with no separate web server.
- The GUI and API are always the same version, so no compatibility matrix
  between them is needed.
- Upgrading the server migrates the database automatically, so users must
  back up their database before upgrading (stated in every release note).
  Downgrades are not supported once migrations have run.
- Building the server requires building the GUI first (`task build:server`).

## Alternatives considered

- **Serve the GUI from a separate container or CDN** — more components to
  deploy and version, and a third-party dependency for self-hosted installs.
- **Manual migration command** — easy to forget, leaving the schema out of
  sync with the binary.

## References

- `server/cmd/server/embed.go`, `server/internal/api/gui.go`,
  `server/internal/db/db.go`, `.goreleaser.yml`.
