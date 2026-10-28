# 0012. Restrict OIDC sign-in by group and cap OIDC session lifetime

- **Status:** Proposed
- **Date:** 2026-10-28 (retro-documented; implemented for issue #277)
- **Deciders:** Arkeep maintainers

## Context

An identity provider (Keycloak, Authentik, Zitadel…) is often shared by many
applications, and most of its users should have no access to a backup system
that can restore data and sees the whole infrastructure. Before this decision,
anyone who could authenticate against a configured provider was provisioned in
Arkeep, and roles could only be managed in Arkeep itself.

Sessions follow [0008](0008-short-lived-jwt-and-refresh-cookie-sessions.md): a
refresh token valid 7 days, rotated on every use. A session kept in use
therefore never expires, so a user signed in through a provider never goes
back to it, and a change made at the provider (removing the user from a group)
would never be seen. This record extends 0008 for OIDC sessions; it does not
replace it.

## Decision

- Each OIDC provider can restrict sign-in and map roles from the user's groups:
  - `groups_claim` names the claim holding the groups, read from the ID token
    and, when absent there, from UserInfo for the same subject. The claim may
    be a list of names, a single name, or an object whose keys are the names.
    Names are compared exactly;
  - `allowed_groups`: when set, only members of at least one of these groups,
    or of an admin group, may sign in;
  - `admin_groups`: when set, members get the `admin` role and every other
    user of the provider the `user` role, re-evaluated on every login. The
    last active admin is never demoted;
  - `groups_claim` is required as soon as a group is set. With no group set,
    the provider behaves as before.
- The group check runs **before** an account is provisioned or linked by
  email. A missing groups claim **fails closed**. A denied login also revokes
  the sessions of the account already linked to that identity (never one
  matched by email). The user sees a generic reason; the details are logged.
- Every session started through an OIDC provider has an **absolute deadline**,
  `session_max_hours` after the login (per provider, 1 to 168 hours, default
  12). It is stored on the refresh token, copied on every rotation, caps the
  token's expiry, and is carried through the two-factor challenge when the
  account has one. Past it, the refresh is refused and the user must sign in
  at the provider again, where the groups are checked. Local logins have no
  deadline and keep the behaviour of 0008.

## Consequences

- A change of groups at the provider takes effect at the user's next OIDC
  login, at the latest `session_max_hours` after the previous one. To cut
  access at once, an admin also disables the user in Arkeep, which takes
  effect within the 15-minute access token lifetime (0008).
- When `admin_groups` is set, the provider decides the role of its users: a
  role changed by hand in Arkeep is overwritten at the next login. A groups
  claim missing because of a provider misconfiguration demotes the OIDC
  admins, except the last active one.
- On upgrade, the open sessions of OIDC-linked accounts end 12 hours later,
  since their original login time was never recorded.
- Group settings and the session cap are recorded in the audit log of
  provider changes.
- Covered by `server/internal/auth/oidc_test.go`, `tokens_test.go`, and the
  end-to-end tests with a fake provider in
  `server/internal/api/oidc_groups_test.go`.

## Alternatives considered

- **Leave access control to the provider** (realm or client access policies)
  — not available or convenient on every provider; the issue asks for it to
  be enforceable in Arkeep.
- **Check only at login, without a session cap** — a session in use never
  returns to the provider, so a removed user could keep access indefinitely.
- **Re-check the groups at the provider on every refresh** — the most precise,
  but needs the provider's refresh token stored encrypted and a provider call
  every 15 minutes: more complexity and one more secret to keep.
- **One global session cap** instead of one per provider — a cap per provider
  sits next to the group settings it serves and lets internal and external
  providers differ.
- **Deactivate users removed from the allowed groups** — disabling an account
  is left to an explicit admin action.

## References

- Issue #277, pull request #350.
- `server/internal/auth/oidc.go`, `server/internal/auth/tokens.go`,
  `server/internal/api/settings.go`, `server/internal/api/auth.go`.
- Migrations `000037_oidc_group_restrictions` and `000038_oidc_session_limit`.
