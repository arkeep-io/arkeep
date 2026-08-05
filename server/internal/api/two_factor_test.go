package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"

	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/db"
)

// TestTwoFactorSchema asserts migration 000018 applied. newTestDeps runs every
// migration against a fresh in-memory SQLite database, so a broken migration
// fails here (and in every other api test) rather than at runtime.
func TestTwoFactorSchema(t *testing.T) {
	deps := newTestDeps(t)
	m := deps.gdb.Migrator()

	for _, col := range []string{"totp_secret", "two_factor_enabled"} {
		if !m.HasColumn(&db.User{}, col) {
			t.Errorf("users.%s column is missing", col)
		}
	}

	for _, tbl := range []string{"two_factor_challenges", "recovery_codes"} {
		if !m.HasTable(tbl) {
			t.Errorf("table %s is missing", tbl)
		}
	}
}

// enable2FA turns on two-factor for an existing user and returns the TOTP
// secret so tests can generate valid codes.
func enable2FA(t *testing.T, deps *testDeps, userID uuid.UUID) string {
	t.Helper()
	secret, _, err := auth.NewTOTPKey("tfa@test.local")
	if err != nil {
		t.Fatalf("enable2FA: NewTOTPKey: %v", err)
	}
	user, err := deps.users.GetByID(context.Background(), userID)
	if err != nil {
		t.Fatalf("enable2FA: GetByID: %v", err)
	}
	user.TOTPSecret = db.EncryptedString(secret)
	user.TwoFactorEnabled = true
	if err := deps.users.Update(context.Background(), user); err != nil {
		t.Fatalf("enable2FA: Update: %v", err)
	}
	return secret
}

// currentCode returns a TOTP code valid right now for secret.
func currentCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("currentCode: %v", err)
	}
	return code
}

// loginStepOne posts credentials and decodes the challenge response.
func loginStepOne(t *testing.T, e *testEnv, email string) loginStepOneResult {
	t.Helper()
	resp := e.post(t, "/api/v1/auth/login", "", map[string]string{
		"email":    email,
		"password": "test-password-123",
	})
	assertStatus(t, resp, http.StatusOK)
	var out loginStepOneResult
	decodeData(t, resp, &out)
	return out
}

type loginStepOneResult struct {
	AccessToken       string `json:"access_token"`
	TwoFactorRequired bool   `json:"two_factor_required"`
	ChallengeToken    string `json:"challenge_token"`
}

func TestTwoFactorLogin(t *testing.T) {
	t.Run("login without 2FA is unchanged", func(t *testing.T) {
		e := newTestEnv(t)
		createDBUser(t, e.deps, "plain@test.local", "user")

		out := loginStepOne(t, e, "plain@test.local")
		if out.TwoFactorRequired {
			t.Error("two_factor_required = true, want false")
		}
		if out.AccessToken == "" {
			t.Error("access_token is empty")
		}
	})

	t.Run("2FA account gets a challenge instead of a token", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "tfa@test.local", "user")
		enable2FA(t, e.deps, id)

		out := loginStepOne(t, e, "tfa@test.local")
		if !out.TwoFactorRequired {
			t.Fatal("two_factor_required = false, want true")
		}
		if out.ChallengeToken == "" {
			t.Fatal("challenge_token is empty")
		}
		if out.AccessToken != "" {
			t.Error("access_token must not be issued at step one")
		}

		// A login stopped at the first factor is not a login.
		user, err := e.deps.users.GetByID(context.Background(), id)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if user.LastLoginAt != nil {
			t.Error("last_login_at was stamped before the second factor")
		}
	})

	t.Run("valid TOTP code completes the login", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "tfa@test.local", "user")
		secret := enable2FA(t, e.deps, id)

		out := loginStepOne(t, e, "tfa@test.local")
		resp := e.post(t, "/api/v1/auth/login/2fa", "", map[string]string{
			"challenge_token": out.ChallengeToken,
			"code":            currentCode(t, secret),
		})
		assertStatus(t, resp, http.StatusOK)

		var final loginStepOneResult
		decodeData(t, resp, &final)
		if final.AccessToken == "" {
			t.Error("access_token is empty after successful 2FA")
		}

		user, err := e.deps.users.GetByID(context.Background(), id)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if user.LastLoginAt == nil {
			t.Error("last_login_at was not stamped on completion")
		}
	})

	t.Run("wrong code returns 400 and the challenge survives", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "tfa@test.local", "user")
		secret := enable2FA(t, e.deps, id)

		out := loginStepOne(t, e, "tfa@test.local")

		bad := e.post(t, "/api/v1/auth/login/2fa", "", map[string]string{
			"challenge_token": out.ChallengeToken,
			"code":            "000000",
		})
		// 400 not 401: the GUI's api() wrapper silently retries 401s.
		assertStatus(t, bad, http.StatusBadRequest)

		good := e.post(t, "/api/v1/auth/login/2fa", "", map[string]string{
			"challenge_token": out.ChallengeToken,
			"code":            currentCode(t, secret),
		})
		assertStatus(t, good, http.StatusOK)
	})

	t.Run("challenge is single use", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "tfa@test.local", "user")
		secret := enable2FA(t, e.deps, id)

		out := loginStepOne(t, e, "tfa@test.local")
		body := map[string]string{"challenge_token": out.ChallengeToken, "code": currentCode(t, secret)}

		assertStatus(t, e.post(t, "/api/v1/auth/login/2fa", "", body), http.StatusOK)
		assertStatus(t, e.post(t, "/api/v1/auth/login/2fa", "", body), http.StatusBadRequest)
	})

	t.Run("expired challenge is rejected", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "tfa@test.local", "user")
		secret := enable2FA(t, e.deps, id)

		raw, err := auth.GenerateResetToken()
		if err != nil {
			t.Fatalf("GenerateResetToken: %v", err)
		}
		if err := e.deps.challenges.Create(context.Background(), &db.TwoFactorChallenge{
			UserID:    id,
			TokenHash: auth.HashToken(raw),
			ExpiresAt: time.Now().Add(-time.Minute),
		}); err != nil {
			t.Fatalf("Create challenge: %v", err)
		}

		resp := e.post(t, "/api/v1/auth/login/2fa", "", map[string]string{
			"challenge_token": raw,
			"code":            currentCode(t, secret),
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("five wrong codes burn the challenge", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "tfa@test.local", "user")
		secret := enable2FA(t, e.deps, id)

		out := loginStepOne(t, e, "tfa@test.local")
		for i := 0; i < 5; i++ {
			resp := e.post(t, "/api/v1/auth/login/2fa", "", map[string]string{
				"challenge_token": out.ChallengeToken,
				"code":            "000000",
			})
			assertStatus(t, resp, http.StatusBadRequest)
		}

		// The correct code no longer helps — the password step must be repeated.
		resp := e.post(t, "/api/v1/auth/login/2fa", "", map[string]string{
			"challenge_token": out.ChallengeToken,
			"code":            currentCode(t, secret),
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("a new login invalidates the previous challenge", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "tfa@test.local", "user")
		secret := enable2FA(t, e.deps, id)

		first := loginStepOne(t, e, "tfa@test.local")
		second := loginStepOne(t, e, "tfa@test.local")

		stale := e.post(t, "/api/v1/auth/login/2fa", "", map[string]string{
			"challenge_token": first.ChallengeToken,
			"code":            currentCode(t, secret),
		})
		assertStatus(t, stale, http.StatusBadRequest)

		fresh := e.post(t, "/api/v1/auth/login/2fa", "", map[string]string{
			"challenge_token": second.ChallengeToken,
			"code":            currentCode(t, secret),
		})
		assertStatus(t, fresh, http.StatusOK)
	})
}
