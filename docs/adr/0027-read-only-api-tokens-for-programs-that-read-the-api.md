# 0027. Read-only API tokens for programs that read the API

- **Status:** Accepted
- **Date:** 2026-11-23
- **Deciders:** Arkeep maintainers

## Context

Programs want to read Arkeep's state: a Home Assistant integration showing
each policy's last run, a dashboard, a script. The API only knows user
sessions ([0008](0008-short-lived-jwt-and-refresh-cookie-sessions.md)): a
password login gives a 15-minute access token and a refresh cookie scoped to
`/api/v1/auth`. A program would have to store a user's password, replay the
login and the refresh, and it cannot log in at all when the account uses
two-factor authentication or OIDC. `/metrics` has no per-policy data, and the
webhook only reports events, with no state to read after a restart.

## Decision

- Users create **API tokens** on their profile. A token is a random string of
  256 bits prefixed with `ark_`, shown once at creation. Only its SHA-256 hash
  is stored, with a name, a hint (the prefix and 4 characters), an optional
  expiry (1 to 3650 days, or none), the time of its last use and its scope.
- The only scope is **`read`**: the token authenticates `GET` requests with
  the rights of the **`user` role**, whatever its owner's role.
  Any other method is refused with 403 (`api_token_read_only`), so a token can
  never change anything, even on a route open to the `user` role. An admin's
  token cannot read admin-only routes either.
- A token is sent as `Authorization: Bearer ark_…`, on the same routes as a
  session. `Authenticate` tells the two apart by the prefix.
- A token stops working when it expires, when it is revoked (its row is
  deleted), while its owner is disabled, and when its owner is deleted.
- The events that end every session of a user because the account may be in
  someone else's hands also revoke the user's tokens: an admin's Set password,
  a password reset by email, an admin's two-factor reset, and an OIDC login
  refused by the provider's allowed groups. A token created with a stolen
  session does not outlive the clean-up. A password change from the profile
  keeps them, as it keeps the session that made it.
- Tokens are managed from a session only: a token can neither list, create
  nor revoke tokens (`RequireSession`). Users manage their own; admins list
  and revoke everyone's (Settings → API Tokens). Creating and revoking a token
  are written to the audit log.
- A user holds at most 50 tokens.

## Consequences

- A Home Assistant integration, a dashboard or a script can read Arkeep with a
  credential that cannot change it and that is revoked without touching the
  user's password or sessions.
- A leaked token exposes what the `user` role reads, including job logs,
  until it is revoked or expires. Its hash cannot be reversed, and the hint is
  too short to guess the rest.
- A token is not bound to the lifetime of an OIDC session
  (`session_max_hours`): a user removed from the allowed groups at the
  identity provider keeps their tokens until their next login is refused.
  To cut access at once, an admin disables the user in Arkeep.
- `last_used_at` is written at most once a minute per token and per server
  process.
- The WebSocket endpoint still takes a session's JWT only: a program polls.
- A later scope can let a token start operations (for example, run a policy
  from a Home Assistant automation). It will need its own decision: which
  operations, and how they are limited.

## Alternatives considered

- **Long-lived JWTs** — cannot be revoked one by one without a denylist kept
  forever, and would carry the owner's role.
- **Tokens carrying the owner's role** — an admin's token, stored in another
  program, would be an admin credential. Read-only programs do not need it.
- **Service accounts** — a user without a password that only holds tokens.
  More to manage, and a user with the `user` role and a token already gives
  the same access.
- **MQTT, or Home Assistant's discovery format** — push state to Home
  Assistant directly. It ties Arkeep to one consumer and adds a broker; tokens
  serve every program.

## References

- `server/internal/auth/api_token.go`, `server/internal/api/api_tokens.go`,
  `server/internal/api/middleware.go` (`Authenticate`, `RequireSession`),
  `server/internal/db/migrations/000048_api_tokens.up.sql`.
- `gui/src/components/users/ApiTokensSection.vue`,
  `gui/src/pages/settings/ApiTokensPage.vue`.
