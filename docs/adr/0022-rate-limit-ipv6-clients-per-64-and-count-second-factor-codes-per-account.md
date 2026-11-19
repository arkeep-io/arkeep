# 0022. Rate limit IPv6 clients per /64 and count second-factor codes per account

- **Status:** Accepted
- **Date:** 2026-11-19
- **Deciders:** Arkeep maintainers

## Context

Sign-in, two-factor, password reset, setup and agent enrollment are rate
limited per client address, with in-memory fixed windows (5 sign-ins a
minute, 10 two-factor codes a minute, …). The bucket was the exact address.
An IPv6 client usually holds a whole /64 and can send every request from a
new address, so it had 2^64 budgets: twelve sign-ins from one /64 all got
through.

A two-factor challenge tolerates five wrong codes, but signing in again with
the password gives a fresh one. Someone who knows an account's password could
therefore try codes without any limit but the address one, and guess a
six-digit code in about half an hour from a single /64.

A lock on failed sign-ins per account, passwords and codes alike, was written
and then removed before release: whoever knows an account's email could keep
it locked by failing on purpose.

## Decision

- **IPv6 clients are counted per /64.** The bucket of an IPv6 address is its
  /64 (zone dropped). An IPv4 address, or an IPv4-mapped IPv6 address, is its
  own bucket. `rateLimitKey` applies this to every address rate limit,
  enrollment included. The audit log keeps the full address.
- **Second-factor codes are counted per account.** Every code submitted for a
  challenge, TOTP or recovery code, right or wrong, counts for the account,
  from any address and across challenges: 10 codes within 15 minutes, then the
  second factor of that account answers `429` with `Retry-After` until the
  window ends, without checking the code. The check runs before the
  per-challenge count.
- **Failed passwords are not counted per account.** They stay limited per
  address only.
- The counts live in server memory, like the address rate limits: one server
  replica (ADR 0005), and a restart clears them.

## Consequences

- A six-digit code can be guessed at most 10 times per 15 minutes per
  account, however many addresses or challenges are used.
- The account limit is reachable only after the right password, so it cannot
  be used to lock out someone whose email is all an attacker knows. Someone
  who has the password can block the owner's sign-in for 15 minutes at a
  time; the password then has to be changed anyway. The server logs every
  refusal.
- Password guessing is still limited per address only: a client with many
  /64s (a /48 holds 65,536) gets that many budgets. Strong passwords and the
  second factor remain the defence against it.
- Clients behind one IPv6 /64 share a budget, as clients behind one IPv4 NAT
  already did.
- The GUI says "too many attempts" on a `429` from the two-factor step instead
  of "invalid code".
- New sign-in steps that check a guessable secret count their attempts per
  account too, the same way.

## Alternatives considered

- **Lock the account on failed passwords and codes** — also caps password
  guessing, but lets anyone who knows an email keep its owner locked out. It
  was removed for that reason.
- **Only the /64 bucket** — leaves the code guessable by someone with the
  password and several /64s.
- **A wider IPv6 prefix (/56 or /48)** — fewer budgets for a large holder,
  but unrelated clients of a provider that hands out /64s from a shared /48
  would share one.
- **Store the counts in the database** — survives restarts, at the cost of a
  migration per driver, for a window of 15 minutes.

## References

- `server/internal/api/ratelimit.go` (`rateLimitKey`),
  `server/internal/api/auth.go` (`LoginTwoFactor`),
  `server/internal/api/enroll.go`.
- `gui/src/components/users/TwoFactorCodeForm.vue`.
- `SECURITY.md` (two-factor authentication).
- ADR 0005, ADR 0008.
