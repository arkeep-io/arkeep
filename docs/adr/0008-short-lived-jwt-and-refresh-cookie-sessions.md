# 0008. Short-lived JWT access tokens with refresh cookies

- **Status:** Accepted
- **Date:** 2026-10-12 (retro-documented; the decision predates this record)
- **Deciders:** Arkeep maintainers

## Context

The GUI is a single-page application that talks to the REST API and a
WebSocket. Sessions must survive page reloads, be revocable, and limit the
damage of a stolen token. Users authenticate either with a local account or
through an external identity provider.

## Decision

- Two tokens per session:
  - an **access token**: a JWT signed with **RS256**, valid **15 minutes**,
    kept in browser memory only and sent as a Bearer token;
  - a **refresh token**: valid 7 days, stored in an **`httpOnly` cookie** and in
    the database, and **rotated on every use**.
- Logout deletes the refresh token and adds the access token's JTI to an
  in-memory denylist.
- Authentication sources are **local accounts** (Argon2id passwords) and
  **OIDC** providers configured in the GUI. **TOTP** two-factor, with recovery
  codes, is optional per user and enforced after both kinds of login,
  including OIDC.
- Roles are `admin` and `user`. The `user` role is **read-only on
  everything**. Every write and every agent action (triggering a job,
  restoring, browsing a snapshot…) requires `admin`, enforced at the router
  with `RequireRole("admin")`.

## Consequences

- A stolen access token is useful for at most 15 minutes, and refresh tokens
  are revocable immediately.
- Access tokens cannot be enumerated: revoking all of a user's sessions leaves
  existing access tokens valid until they expire, and the denylist is lost on
  restart. Both limitations are documented in `SECURITY.md`.
- New API routes must declare their role at the router.
  `router_authz_test.go` checks that non-admins cannot reach write routes.
- Current limitation, not part of this decision: the WebSocket receives the
  access token as a query parameter, because browsers cannot set headers on
  WebSocket connections. It is expected to be replaced by a one-time ticket
  without changing this ADR.

## Alternatives considered

- **Server-side sessions only** — simpler revocation, but a database lookup on
  every request and less convenient for the WebSocket and API clients.
- **Long-lived JWT** — impossible to revoke in practice.

## References

- [Security](https://arkeep.io/docs/concepts/security/) on arkeep.io/docs;
  `SECURITY.md` → Session and token revocation.
- `server/internal/auth/`, `server/internal/api/auth.go`,
  `server/internal/api/router.go`, `server/internal/api/router_authz_test.go`.
