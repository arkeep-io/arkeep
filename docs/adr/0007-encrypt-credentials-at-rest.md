# 0007. Encrypt credentials at rest, never return them

- **Status:** Accepted
- **Date:** 2026-10-12 (retro-documented; the decision predates this record)
- **Deciders:** Arkeep maintainers

## Context

The database holds repository passwords, cloud and SFTP credentials, SMTP
passwords, OIDC client secrets and TOTP secrets. Database files and dumps are
copied around (backups, support requests, other hosts), so they must not give
these secrets away on their own.

## Decision

- Sensitive fields are stored as `db.EncryptedString`, encrypted with
  **AES-256-GCM** using the server key `ARKEEP_SECRET_KEY`, required at startup.
- Credentials are **write-only**: API responses never return them after
  creation.
- Deleting a destination or a policy **wipes its secrets** while keeping the
  row for history. SQLite runs with `secure_delete` so wiped values do not
  linger in free pages.
- Recovery codes are stored as SHA-256 hashes, and local passwords with
  Argon2id.

## Consequences

- A database file or dump alone does not expose credentials.
- Anyone who can read both the database and `ARKEEP_SECRET_KEY` (that is, the
  server host) can decrypt everything. The server needs the key to send
  credentials to agents, so this is inherent.
- Losing or changing `ARKEEP_SECRET_KEY` makes stored credentials unreadable,
  so the key must be kept stable and backed up separately from the database.

## Alternatives considered

- **Plain storage, relying on filesystem permissions** — every dump or backup
  would leak all credentials.
- **External secret manager (Vault, KMS)** — an extra mandatory component for
  self-hosters. It remains possible later ("BYOK" is on the roadmap).

## References

- `SECURITY.md` → Credential storage, Two-factor authentication.
- `server/internal/db/encrypt.go`.
