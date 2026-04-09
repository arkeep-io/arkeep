# Security Policy

## Reporting a Vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Please email **hello@arkeep.io** with the following information:

- A clear description of the vulnerability
- Steps to reproduce the issue
- The potential impact (what an attacker could achieve)
- Any suggested mitigations, if you have them

You will receive a response within **48 hours** acknowledging your report.
We will keep you informed of progress and ask that you give us reasonable time
to develop and release a patch before any public disclosure.

## Scope

The following are considered in scope:

- `arkeep-server` — authentication, authorization, credential storage, API endpoints
- `arkeep-agent` — gRPC authentication, binary extraction, restic invocation
- Docker images published to ghcr.io/arkeep-io and Docker Hub

The following are **out of scope**:

- Vulnerabilities in Restic or Rclone themselves — report those upstream
- Issues requiring physical access to the host machine
- Social engineering attacks
- Denial of service via resource exhaustion

## Security Considerations

### Hook commands

Pre/post backup hooks are shell commands executed by the agent process on the target
machine. They run with the **full privileges of the agent process** (typically a
dedicated unprivileged user, but potentially root or docker-group depending on your
deployment).

**Risk:** A malicious or compromised hook command has the same capabilities as direct
shell access to the machine. An admin who can create or edit policies can therefore
execute arbitrary code on any machine running a connected agent.

**Mitigations in place:**

- Only users with the `admin` role can set or modify hook commands.
- The server rejects commands containing command substitution (`$(...)`, backticks),
  path traversal (`..`), and references to internal credential environment variables
  (`$RESTIC_*`, `$RCLONE_*`, `$ARKEEP_*`).
- Hook commands are limited to 1024 characters.

**Deployment recommendation:** Run the agent as a dedicated low-privilege user with
access only to the directories it needs to back up. Avoid running the agent as root
unless strictly required (e.g. bare-metal full-system backup).

### Credential storage

All sensitive values (repository passwords, S3 keys, SFTP passwords, OIDC client
secrets) are encrypted at rest using AES-256-GCM with a server-side encryption key.
Credentials are never returned in API responses — they are write-only after creation.

### Session and token revocation

Arkeep uses two tokens per session:

| Token | TTL | Storage | Revocation |
|-------|-----|---------|------------|
| Access token (JWT) | 15 minutes | Browser memory only | Immediate via in-memory denylist |
| Refresh token | 7 days | `httpOnly` cookie | Immediate via database deletion |

**On logout**, both tokens are revoked simultaneously: the refresh token is deleted from
the database and the access token's JTI is added to an in-memory denylist. Any
subsequent request using either token is rejected immediately.

**Known limitation — `LogoutAllSessions`:** when an admin revokes all sessions for a
user (e.g. after a password change or account suspension), all refresh tokens are
deleted from the database. However, access tokens already issued cannot be enumerated
retroactively because JWTs are stateless — their JTIs are not stored server-side.
Active access tokens will remain valid until they expire naturally (≤ 15 minutes).

If this window is unacceptable for your threat model, mitigations include:

- Reducing `accessTokenDuration` in `server/internal/auth/jwt.go` (e.g. to 5 minutes).
- Treating admin-initiated session revocation as requiring a short propagation delay
  rather than an instant cut-off.
- Running the server behind a reverse proxy that can enforce an emergency block at the
  network layer.

**Denylist persistence:** the access token denylist is in-memory and is cleared on
server restart. Tokens revoked shortly before a restart may be accepted for up to
their remaining TTL after the restart. Refresh token revocation is unaffected — it is
persisted in the database.

### Metrics endpoint

The `/metrics` endpoint (Prometheus text format) is **unauthenticated** and accessible on the same port as the HTTP API (default `:8080`). It does not expose credentials, backup contents, or user data — only operational counters and gauges (job counts, agent connections, HTTP request rates).

However, the endpoint can reveal information about your deployment (number of agents, job frequency, error rates) that you may not want public.

**Recommendation:** restrict access at the reverse-proxy or firewall level so only your Prometheus scraper can reach it:

```nginx
location /metrics {
    allow 10.0.0.0/8;   # your internal network / Prometheus scraper IP
    deny all;
}
```

If Arkeep is not behind a reverse proxy, use firewall rules to block port 8080 from untrusted sources, or run Prometheus on the same host and bind it to `localhost`.

### Agent authentication

Agents authenticate to the server via mutual TLS (mTLS) using certificates issued by
an auto-generated private CA on first startup. The CA and server certificates are
stored in the server's data directory. Keep this directory secure.

## Supported Versions

Security fixes are applied to the latest released version only.
We do not backport fixes to older versions.

| Version | Supported |
|---|---|
| Latest | ✓ |
| Older | ✗ |