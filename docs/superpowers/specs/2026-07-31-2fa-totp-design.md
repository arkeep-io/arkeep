# Two-Factor Authentication (TOTP) — Design

**Issue:** #195
**Branch:** `feat/195-add-the-2fa-feature`
**Date:** 2026-07-31

## Context

Arkeep authenticates local accounts with a single factor: email + password, hashed with Argon2id
(`server/internal/auth/local.go:193`) and encrypted at rest via `EncryptedString`. OIDC exists as an SSO
alternative, but it delegates authentication entirely to the identity provider. An attacker who obtains a
password owns the account, and with it every backup repository password and destination credential the
instance holds.

This spec adds TOTP-based two-factor authentication for local accounts, with recovery codes and an
administrative reset path.

### Scope

**In scope**

- TOTP enrollment, activation and removal (self-service, from the user profile)
- Two-step login for accounts with 2FA enabled
- Recovery codes (single-use, regenerable)
- Administrative 2FA reset for a locked-out user
- Fix for `RevokeAllForUser`, which is currently a no-op (see below)

**Out of scope — separate issues**

- Instance-wide `require_2fa` enforcement
- Trusted devices ("remember this browser")
- WebAuthn / passkeys

### Why not a challenge JWT

An earlier estimate proposed carrying the interim login state in a JWT with a `scope: 2fa_pending` claim.
Reading the code, that design is unsafe as specified:

- `auth.Claims` (`server/internal/auth/jwt.go:28-51`) carries only `uid`, `email` and `role`. There is no
  scope concept, and `Authenticate` (`server/internal/api/middleware.go:33-58`) accepts any token that
  `ValidateAccessToken` approves. A pre-authentication token signed with the same key would therefore be
  accepted as a full access token — a complete bypass of the second factor. Making it safe requires adding a
  scope claim *and* validating it in every path that consumes a token.
- `buildJWTManager` (`server/cmd/server/main.go:392-412`) generates ephemeral RSA keys when
  `{data-dir}/jwt_private.pem` is absent, so challenges would be invalidated by every restart.

This design uses an opaque, database-backed challenge token instead, modelled on `password_reset_tokens`.
It cannot be confused with an access token, it survives restarts, and — decisively — it gives us a row on
which to count failed attempts.

---

## Architecture

### Login flow

```
POST /api/v1/auth/login  {email, password}
      │
      ├─ password invalid ──────────────────► 401
      │
      ├─ 2FA disabled ─────────────────────► 200 {access_token, expires_in}
      │                                        + Set-Cookie arkeep_refresh_token
      │
      └─ 2FA enabled ──────────────────────► 200 {two_factor_required: true,
                                                  challenge_token: "<64 hex>"}
                                               └─ row in two_factor_challenges
                                                  (SHA-256 hash only, TTL 5 min,
                                                   attempts = 0)

POST /api/v1/auth/login/2fa  {challenge_token, code}
      │
      ├─ challenge unknown / expired / used ─► 400
      ├─ code invalid ──────────────────────► 400, attempts++
      │                                        (at 5 → challenge burned)
      └─ code valid ────────────────────────► 200 {access_token, expires_in}
                                               + Set-Cookie arkeep_refresh_token
                                               + challenge marked used
```

`code` accepts either a TOTP value or a recovery code. A value matching `^[0-9]{6}$` is treated as TOTP;
anything else is normalised (dashes stripped, upper-cased) and looked up as a recovery code.

The challenge is created only *after* the password has been verified, so the endpoint reveals nothing about
which accounts exist or which have 2FA enabled.

**Error status is 400, not 401, on an invalid code.** The GUI's `api()` wrapper
(`gui/src/services/api.ts:60-102`) intercepts 401 responses, attempts a silent token refresh and retries the
request. A 401 here would be swallowed by the client. Returning 400 keeps the endpoint correct even if a
caller routes it through `api()` by mistake.

### Enrollment flow

```
POST /api/v1/auth/2fa/setup     → {secret, otpauth_url}
                                   stored in users.totp_secret,
                                   two_factor_enabled stays false  ("pending")
        user scans QR / types secret into their authenticator
POST /api/v1/auth/2fa/verify    {code}
                                → two_factor_enabled = true
                                → 10 recovery codes, returned once and never again
                                → other sessions revoked
```

A pending secret is simply a non-empty `totp_secret` with `two_factor_enabled = false`. No extra table is
needed, and an abandoned enrollment is harmlessly overwritten by the next `setup` call.

---

## Backend — `server/`

### Dependency

`github.com/pquerna/otp` — the only new Go dependency. Provides secret generation, TOTP validation with a
configurable skew window, and `otpauth://` URI construction. No Go QR dependency: the QR is rendered in the
browser.

### Migration `000018_two_factor`

Placed in the shared `server/internal/db/migrations/` directory, not in a per-driver subdirectory: the
statements are `ADD COLUMN` with defaults and `CREATE TABLE`, with no `CHECK` constraints and no table
rebuild, so the SQL is identical on SQLite and PostgreSQL. `000017` is the last shared migration.

`up`:

```sql
-- Migration: 000018_two_factor
-- Adds TOTP-based two-factor authentication for local accounts.
--
-- users.totp_secret is typed EncryptedString in Go (AES-256-GCM at rest, same as
-- users.password and oidc_providers.client_secret). An empty string means "no
-- secret"; a non-empty secret with two_factor_enabled = false means enrollment
-- was started but never confirmed.
--
-- two_factor_challenges holds the short-lived interim state between a successful
-- password check and a successful TOTP check. Only the SHA-256 hash of the
-- challenge token is stored. The attempts column is the per-account lockout:
-- Arkeep has no other account-level throttle, and a per-IP rate limit alone is
-- weak against a six-digit code.
--
-- recovery_codes are single-use (used_at) and stored hashed. Rows in both tables
-- are removed automatically when the parent user is deleted (ON DELETE CASCADE).

ALTER TABLE users ADD COLUMN totp_secret        TEXT    NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN two_factor_enabled BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS two_factor_challenges (
    id          TEXT        NOT NULL PRIMARY KEY,
    user_id     TEXT        NOT NULL,
    token_hash  TEXT        NOT NULL,
    attempts    INTEGER     NOT NULL DEFAULT 0,
    expires_at  TIMESTAMP   NOT NULL,
    used_at     TIMESTAMP,
    created_at  TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT fk_tfc_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_tfc_token_hash ON two_factor_challenges (token_hash);
CREATE INDEX IF NOT EXISTS idx_tfc_user_id    ON two_factor_challenges (user_id);

CREATE TABLE IF NOT EXISTS recovery_codes (
    id          TEXT        NOT NULL PRIMARY KEY,
    user_id     TEXT        NOT NULL,
    code_hash   TEXT        NOT NULL,
    used_at     TIMESTAMP,
    created_at  TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT fk_rc_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_rc_code_hash ON recovery_codes (code_hash);
CREATE INDEX IF NOT EXISTS idx_rc_user_id   ON recovery_codes (user_id);
```

`down`: drop both tables, then drop the two columns. `ALTER TABLE ... DROP COLUMN` requires SQLite ≥ 3.35 and
that the column is not indexed — both hold here: the project builds against `modernc.org/sqlite v1.53.0` and
`github.com/mattn/go-sqlite3 v1.14.47`, which bundle SQLite ≥ 3.50, and neither new column is indexed.

### Models — `internal/db/models.go`

`User` (line 48) gains:

```go
TOTPSecret       EncryptedString `gorm:"type:text"`           // empty = no secret
TwoFactorEnabled bool            `gorm:"not null;default:false"`
```

Two new structs, both embedding `Base` (UUIDv7 via `BeforeCreate`), modelled on `PasswordResetToken`
(line 78):

```go
type TwoFactorChallenge struct {
	Base
	UserID    uuid.UUID  `gorm:"type:text;not null;index"`
	TokenHash string     `gorm:"not null;index"`
	Attempts  int        `gorm:"not null;default:0"`
	ExpiresAt time.Time  `gorm:"not null"`
	UsedAt    *time.Time
}

type RecoveryCode struct {
	Base
	UserID   uuid.UUID `gorm:"type:text;not null;index"`
	CodeHash string    `gorm:"not null;index"`
	UsedAt   *time.Time
}
```

### Repositories

New file `internal/repositories/two_factor.go`, following `password_reset.go`. Interfaces declared in
`repositories.go` alongside the existing ones:

```go
type TwoFactorChallengeRepository interface {
	Create(ctx context.Context, c *db.TwoFactorChallenge) error
	GetUnusedByHash(ctx context.Context, hash string) (*db.TwoFactorChallenge, error) // used_at IS NULL
	IncrementAttempts(ctx context.Context, id uuid.UUID) (int, error)                 // returns new count
	MarkUsed(ctx context.Context, id uuid.UUID) error
	DeleteByUserID(ctx context.Context, userID uuid.UUID) error
}

type RecoveryCodeRepository interface {
	CreateBatch(ctx context.Context, codes []db.RecoveryCode) error
	GetUnusedByHash(ctx context.Context, hash string) (*db.RecoveryCode, error)
	MarkUsed(ctx context.Context, id uuid.UUID) error
	CountUnused(ctx context.Context, userID uuid.UUID) (int64, error)
	DeleteByUserID(ctx context.Context, userID uuid.UUID) error
}
```

Single-use is enforced in two layers, exactly as password reset does it: the `used_at IS NULL` predicate in
`GetUnusedByHash`, plus `MarkUsed` stamping `used_at`. Expiry is **not** part of the query — the handler
compares `ExpiresAt` after the lookup, matching `password_reset.go:223`, so an expired challenge and an
invalid one are indistinguishable to the caller.

#### Fixing `RevokeAllForUser`

`RefreshTokenRepository.RevokeAllForUser` (`internal/repositories/user.go:178-187`) sets `revoked_at`, but
`GetByHash` (`user.go:148-158`) does not filter on it and `LocalAuthProvider.RefreshToken`
(`internal/auth/local.go:114-147`) only checks `ExpiresAt`. A revoked refresh token is therefore still
redeemable, which makes the mechanism a no-op. This already silently weakens the session invalidation in
password reset (`internal/api/password_reset.go:261`).

Fix: add `revoked_at IS NULL` to the `GetByHash` predicate, with a regression test asserting a revoked token
cannot be redeemed.

Also add:

```go
RevokeAllForUserExcept(ctx context.Context, userID uuid.UUID, keepHash string) error
```

Needed so that enabling 2FA closes other sessions without logging out the session performing the change.

### TOTP — `internal/auth/totp.go` (new)

```go
// GenerateTOTPSecret returns a base32-encoded 20-byte secret (RFC 4226 §4 recommends >= 128 bits).
func GenerateTOTPSecret() (string, error)

// BuildOTPAuthURL returns otpauth://totp/Arkeep:<email>?secret=...&issuer=Arkeep
func BuildOTPAuthURL(secret, email string) string

// ValidateTOTPCode validates a 6-digit code against secret, accepting a skew of
// +/- 1 period (+/- 30s) to tolerate client clock drift.
func ValidateTOTPCode(secret, code string) bool

// GenerateRecoveryCodes returns n codes of 80 bits each, formatted XXXX-XXXX-XXXX-XXXX (base32).
func GenerateRecoveryCodes(n int) ([]string, error)
```

Recovery codes are stored as `HashToken(normalise(code))` — the existing unsalted SHA-256 helper
(`local.go:218`). Unsalted SHA-256 is appropriate *because* the codes carry 80 bits of entropy; short or
human-chosen codes would require a KDF instead. `normalise` strips dashes and upper-cases, so users may type
them either way.

Constants: `recoveryCodeCount = 10`, `twoFactorChallengeTTL = 5 * time.Minute`,
`maxTwoFactorAttempts = 5`.

### Two-step login

`LocalAuthProvider.Login` (`internal/auth/local.go:78-109`) currently calls `issueTokenPair` immediately
after `verifyPassword`. The provider signature stays unchanged; instead a typed error is returned:

```go
type TwoFactorRequiredError struct{ UserID uuid.UUID }

func (e *TwoFactorRequiredError) Error() string { return "two-factor authentication required" }
```

The check belongs immediately after `verifyPassword` and **before** the `LastLoginAt` update at
`local.go:97-102`: a login halted at the first factor is not a successful login and must not stamp
`last_login_at`. For the same reason the `auth.login` audit record — written today in the handler at
`auth.go:92-96` — moves to the completion of step two.

`AuthHandler.Login` (`internal/api/auth.go:65-103`) intercepts the error with `errors.As`, deletes any
previous challenges for that user, creates a new one, and responds 200. `loginResponse` (`auth.go:58`) gains:

```go
TwoFactorRequired bool   `json:"two_factor_required,omitempty"`
ChallengeToken    string `json:"challenge_token,omitempty"`
```

### Endpoints

Public, registered next to the existing auth routes in `internal/api/router.go:129-157`:

| Method | Path | Body | Success | Failure |
|---|---|---|---|---|
| POST | `/api/v1/auth/login/2fa` | `{challenge_token, code}` | 200 `{access_token, expires_in}` + refresh cookie | 400 on unknown/expired/used challenge or invalid code |

Rate limiting: a **dedicated** `NewRateLimiter(10, time.Minute)`. Do not reuse `loginLimiter`
(`router.go:132`) — it is already shared by five routes on a single cumulative 5/min budget. The real
account-level throttle is the `attempts` counter: each invalid code increments it, and the fifth burns the
challenge via `MarkUsed`, forcing a fresh password step.

The path sits under `/api/v1/auth`, which matters: the refresh cookie is scoped `Path=/api/v1/auth`
(`auth.go:19-26,285`), so it is transmitted correctly.

Authenticated, in the group that already serves `/users/me`:

| Method | Path | Body | Behaviour |
|---|---|---|---|
| GET | `/api/v1/auth/2fa/status` | — | `{enabled, pending, recovery_codes_remaining}` |
| POST | `/api/v1/auth/2fa/setup` | — | Generates a secret, stores it with `two_factor_enabled = false`, returns `{secret, otpauth_url}`. Idempotent while pending: a second call regenerates. 409 if 2FA is already enabled |
| POST | `/api/v1/auth/2fa/verify` | `{code}` | Validates against the pending secret, sets `two_factor_enabled = true`, generates and returns 10 recovery codes **once**, calls `RevokeAllForUserExcept(currentRefreshHash)`. 400 if there is no pending secret or the code is wrong |
| POST | `/api/v1/auth/2fa/disable` | `{password}` | Verifies the password, clears secret and flag, deletes recovery codes and pending challenges |
| POST | `/api/v1/auth/2fa/recovery-codes/regenerate` | `{password}` | Verifies the password, replaces the whole set, returns it once |

Regeneration is in scope deliberately: ten single-use codes are exhaustible, and running out reproduces
precisely the lockout that recovery codes exist to prevent. The GUI surfaces the remaining count so users
notice before that happens.

`RevokeAllForUserExcept` reads the current refresh token from the `arkeep_refresh_token` cookie and hashes it
with `auth.HashToken`. If the cookie is absent, fall back to `RevokeAllForUser` — failing towards revocation
rather than away from it.

**OIDC guard** on `setup`, `verify`, `disable` and `regenerate`, in the same shape as
`password_reset.go:144`: reject with 400 when `user.OIDCProvider != "" || user.Password == ""`. SSO accounts
do their second factor at the identity provider.

Administrative, in the `RequireRole("admin")` group (`router.go:215-250`):

| Method | Path | Behaviour |
|---|---|---|
| POST | `/api/v1/users/{id}/2fa/reset` | Clears secret and flag, deletes recovery codes and challenges, calls `RevokeAllForUser`, audits |

`userResponse` (`internal/api/users.go:41`) gains `two_factor_enabled` so both the admin table and the
profile can render state without an extra call.

### Audit

Reuse `logAudit` / `logAuditDirect` (`internal/api/audit.go`); the `Direct` variant is the correct one for
events that occur before a token exists, which is how `auth.login` already handles itself
(`auth.go:92-96`). New actions:

`auth.2fa.enabled` · `auth.2fa.disabled` · `auth.2fa.challenge_failed` · `auth.2fa.recovery_used` ·
`auth.2fa.recovery_regenerated` · `user.2fa.reset`

### Wiring

`RouterConfig` (`router.go:21-79`) gains the two repositories; the handler is constructed in the aligned
block at `router.go:93-112`; repositories are instantiated and passed in `cmd/server/main.go:171-183` and
`:296-324`. This mirrors exactly how `passwordResetHandler` was introduced.

---

## Frontend — `gui/`

### Dependencies

- `qrcode` (npm, ~15 KB gzipped) — QR rendering
- `shadcn-vue add pin-input` — generates `src/components/ui/pin-input/`. **No new dependency**: `reka-ui@2.10`
  already ships the `PinInput` primitive, it simply has no wrapper yet

### Two-step login

`stores/auth.ts:52-58` — `login()` returns `void` today and throws on any non-2xx. New contract:

```ts
type LoginOutcome = { twoFactorRequired: false } | { twoFactorRequired: true; challengeToken: string }
async function login(email: string, password: string): Promise<LoginOutcome>
```

Call sites to update: `pages/auth/LoginPage.vue:67-75` and `pages/SetupPage.vue:66-85` (the setup flow
auto-logs-in the first admin, who cannot have 2FA yet, but the type must still be handled).

New action `completeTwoFactor(challengeToken, code)` calling `/api/v1/auth/login/2fa` with **raw `ofetch`, not
`api()`** — the same deliberate choice already made for `login()` and `refresh()`, and doubly important here
given the 401-interception behaviour described above.

`LoginPage.vue` gains a local `step` of `'credentials' | 'code'`. Step two renders a 6-digit `PinInput` plus a
"Use a recovery code" link that swaps in a plain `Input`. Reuse the page's existing chrome — `Card`,
`FieldGroup`, `Alert variant="destructive"`, `Loader2 animate-spin` — and its existing vee-validate + zod
setup.

### Profile section

`pages/users/ProfilePage.vue` — a fourth block in the established
`grid grid-cols-[280px_1fr] gap-12 py-8 border-b` layout, after Password. Three states:

1. **Disabled** — description + "Enable" button
2. **Enrolling** — QR code, the base32 secret as a manual fallback, `PinInput` to confirm, Cancel
3. **Enabled** — status badge, "N recovery codes remaining", Regenerate, Disable (password-confirmed)

Recovery codes are shown in a `Dialog` with copy-to-clipboard (`useClipboard` from `@vueuse/core`, already a
dependency) and a `.txt` download, with an explicit "these are shown only once" warning.

Gate OIDC accounts by reusing the existing `isOIDC` computed (line 31) and the dashed-border placeholder the
password section already uses for them (lines 222-225).

Note: this page does **not** use vee-validate — it uses plain refs with manual `zod.safeParse` and an error
map (lines 44-56, 91-112). Follow the local convention in each file. There is no toast system; feedback is an
inline `<Alert>` cleared by `setTimeout`.

### Admin

`pages/users/UsersPage.vue` — a "2FA" badge in the table and a "Reset 2FA" entry in the existing
`MoreHorizontal` dropdown, confirmed through `AlertDialog` following the delete pattern at lines 123-144.

### Types

`src/types/index.ts` — `two_factor_enabled` on `User` (line 73), the challenge fields on `TokenResponse`
(line 374), and new interfaces for the setup, status and recovery payloads.

---

## Testing

### Backend

New `internal/api/two_factor_test.go`, modelled on `password_reset_test.go`, using the existing harness:
`newTestEnv`, `e.post`, `e.tokenForUser`, `createDBUser`, `assertStatus`, `decodeData`. Register the two new
repositories in `testDeps` (`testhelpers_test.go:62-100`) and the corresponding `RouterConfig` fields. Use
`newTestLimiter` (`ratelimit_test.go:96-101`) so limiter goroutines are cleaned up.

Cases:

- TOTP valid · invalid · accepted at ±1 period drift
- challenge expired → 400
- challenge single-use: a second successful redemption is rejected
- five invalid codes burn the challenge; the sixth attempt fails even with a correct code
- recovery code accepted once, rejected the second time
- `CountUnused` decrements after use
- OIDC account rejected from `setup`
- `disable` without the correct password rejected
- admin reset rejected for role `user`, accepted for `admin`
- regression: login without 2FA unchanged; OIDC login unchanged
- regression: a revoked refresh token can no longer be redeemed (the `GetByHash` fix)

Plus `internal/auth/totp_test.go` for `ValidateTOTPCode`, `GenerateRecoveryCodes` (count, format, uniqueness)
and `BuildOTPAuthURL`.

```bash
cd server && go test ./...                        # baseline ~351 tests
go test ./internal/api/ -run TestTwoFactor -v
go test ./internal/auth/ -v
```

### Manual end-to-end

1. Enable 2FA from the profile; scan the QR with a real authenticator; confirm; save the recovery codes.
2. Log out, log in: verify the challenge step appears, the code is accepted, and the session works.
3. Log out, log in with a recovery code; verify the same code is rejected on a second attempt and the
   remaining count drops.
4. Exhaust five wrong codes; verify the challenge is burned and the password step must be repeated.
5. As an admin, reset another user's 2FA; verify that user logs in with password only afterwards.
6. Disable 2FA from the profile; verify login returns to single-step.
7. Confirm an OIDC account sees the "managed by your provider" placeholder, not the enrollment UI.
8. Start the server against both SQLite and PostgreSQL and confirm `000018` applies cleanly.

### Frontend

No frontend test infrastructure exists — there is no vitest, no test script, and no component tests anywhere
in `gui/`. This work follows the existing convention and is verified manually. Introducing vitest is a
separate issue, not a prerequisite.

---

## Risks and notes

1. `decodeJSON` sets `DisallowUnknownFields` (`internal/api/response.go:142-154`). Every new request field
   must be declared in its struct or clients receive 400.
2. SQLite `ALTER TABLE ... DROP COLUMN` support — see the down migration above.
3. Rate limiters never call `Stop()` anywhere in the codebase, leaking one goroutine each for the process
   lifetime. Consistent with current behaviour and accepted here; tests must use `newTestLimiter`.
4. Documentation to update by hand: the feature table in `README.md:112-135` (currently
   `| Local auth + OIDC | ✓ |`, with no 2FA row) and `SECURITY.md`. There is no OpenAPI spec and no
   hand-maintained CHANGELOG — release notes are generated from Conventional Commits by `.goreleaser.yml`.
5. Commits follow Conventional Commits with the issue number appended, e.g.
   `feat: add TOTP two-factor authentication #195`.

## Effort

Approximately **2.5–3.5 person-days** for a developer familiar with the codebase. The password-reset feature
is a near-complete blueprint for the token/hash/repository/rate-limited-handler mechanics, and there is a
single login choke point to modify, so architectural risk is low.
