# 0017. Trust only loopback proxies by default and lock accounts after failed sign-ins

- **Status:** Proposed
- **Date:** 2026-11-12
- **Deciders:** Arkeep maintainers

## Context

The login, two-factor and password reset routes are rate limited per client
address. Behind a reverse proxy every client used to share the proxy's
address, so the server reads the client from `X-Forwarded-For` when the
request comes from a trusted proxy (`ARKEEP_TRUSTED_PROXIES`).

With the variable empty, the server trusted loopback and every private
network. Anyone able to reach the server from one of them (the LAN, another
container, any pod in the cluster, and with rootless Docker or Podman possibly
internet clients too, since they can appear with a private source address)
could name a new client address in every request and never hit a rate limit.
The audit log recorded the forged address.

The two-factor step has its own limit of five codes per challenge, but the
counter was read before the code was checked and written after, so parallel
requests got more codes checked, and signing in again with the password
gave a fresh challenge with five more. Parallel requests with a right code
could each get a session from the same challenge.

## Decision

- With `ARKEEP_TRUSTED_PROXIES` empty, only loopback (`127.0.0.0/8`, `::1`)
  is trusted. A proxy in another container, pod or host must be listed
  explicitly. `none` still trusts no proxy.
- Failed sign-ins are also counted per account, whatever address they come
  from: 10 failures within 15 minutes lock the account's sign-in for
  15 minutes. Wrong passwords and wrong second factors count together, so a
  fresh two-factor challenge does not reset the count. A locked account gets
  `429` with `Retry-After`, without the credentials being checked. A
  completed sign-in clears the count.
- Accounts are keyed by the email typed at sign-in, lowercased, existing or
  not, so a locked unknown address answers like a locked real one.
- The count lives in server memory. This relies on the single server replica
  of ADR 0005; a restart clears it.
- A two-factor attempt is counted before the code is checked, in one
  conditional update (`attempts < max`, challenge unused), and a challenge or
  recovery code is consumed only if it is still unused. Rules for new sign-in
  steps: count the attempt atomically before checking the secret, and consume
  single-use tokens with a conditional update.

## Consequences

- Rotating `X-Forwarded-For` or signing in again no longer buys attempts:
  a password or a six-digit code can be guessed at most 10 times per
  15 minutes per account.
- Whoever knows an account's email can keep it locked by failing on purpose.
  The lock is short, the per-address rate limits still apply, and the server
  logs every lock. An OIDC sign-in skips the password step but not the
  two-factor one, which checks the lock too.
- Installations with the proxy in another container or behind a Kubernetes
  ingress controller must set `ARKEEP_TRUSTED_PROXIES` after upgrading,
  otherwise every client shares the proxy's address. The server logs a
  warning the first time it ignores `X-Forwarded-For` from an untrusted peer.
  The release notes, the deployment files and the documentation say so.
- With several server replicas the per-account count would be per replica.
  Running more than one replica is already unsupported (ADR 0005).

## Alternatives considered

- **Keep trusting private networks** — the per-account lock would cap
  guessing, but rate limits and audit addresses would stay forgeable from
  every private network.
- **Trust no proxy by default** — breaks a reverse proxy on the same host,
  the most common setup, for no gain: only local processes can use loopback.
- **Store the count in the database** — survives restarts, but needs a
  migration per driver. A restart already takes longer than it gives back.
- **Lock until an administrator unlocks** — an attacker could lock every
  administrator out for good.

## References

- `server/internal/api/realip.go` (`DefaultTrustedProxies`).
- `server/internal/api/login_lockout.go`, `server/internal/api/auth.go`
  (`Login`, `LoginTwoFactor`).
- `server/internal/repositories/two_factor.go` (`ReserveAttempt`, `Consume`).
- ADR 0005, ADR 0008.
