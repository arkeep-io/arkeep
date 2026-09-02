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

type twoFactorStatus struct {
	Enabled                bool  `json:"enabled"`
	Pending                bool  `json:"pending"`
	RecoveryCodesRemaining int64 `json:"recovery_codes_remaining"`
}

type twoFactorSetup struct {
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauth_url"`
}

type twoFactorRecoveryCodes struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

func TestTwoFactorEnrollment(t *testing.T) {
	t.Run("status starts disabled", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "u@test.local", "user")
		token := e.tokenForUser(t, id, "user")

		resp := e.get(t, "/api/v1/auth/2fa/status", token)
		assertStatus(t, resp, http.StatusOK)
		var out twoFactorStatus
		decodeData(t, resp, &out)
		if out.Enabled || out.Pending {
			t.Errorf("status = %+v, want disabled and not pending", out)
		}
	})

	t.Run("setup then verify enables 2FA and returns recovery codes", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "u@test.local", "user")
		token := e.tokenForUser(t, id, "user")

		setupResp := e.post(t, "/api/v1/auth/2fa/setup", token, nil)
		assertStatus(t, setupResp, http.StatusOK)
		var setup twoFactorSetup
		decodeData(t, setupResp, &setup)
		if setup.Secret == "" || setup.OTPAuthURL == "" {
			t.Fatalf("setup response missing secret/otpauth_url: %+v", setup)
		}

		verifyResp := e.post(t, "/api/v1/auth/2fa/verify", token, map[string]string{
			"code": currentCode(t, setup.Secret),
		})
		assertStatus(t, verifyResp, http.StatusOK)
		var codes twoFactorRecoveryCodes
		decodeData(t, verifyResp, &codes)
		if len(codes.RecoveryCodes) != auth.RecoveryCodeCount {
			t.Errorf("got %d recovery codes, want %d", len(codes.RecoveryCodes), auth.RecoveryCodeCount)
		}

		statusResp := e.get(t, "/api/v1/auth/2fa/status", token)
		var status twoFactorStatus
		decodeData(t, statusResp, &status)
		if !status.Enabled {
			t.Error("status.enabled = false after verify, want true")
		}
		if status.RecoveryCodesRemaining != int64(auth.RecoveryCodeCount) {
			t.Errorf("recovery_codes_remaining = %d, want %d", status.RecoveryCodesRemaining, auth.RecoveryCodeCount)
		}
	})

	t.Run("wrong code does not enable 2FA", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "u@test.local", "user")
		token := e.tokenForUser(t, id, "user")

		setupResp := e.post(t, "/api/v1/auth/2fa/setup", token, nil)
		var setup twoFactorSetup
		decodeData(t, setupResp, &setup)

		resp := e.post(t, "/api/v1/auth/2fa/verify", token, map[string]string{"code": "000000"})
		assertStatus(t, resp, http.StatusBadRequest)

		statusResp := e.get(t, "/api/v1/auth/2fa/status", token)
		var status twoFactorStatus
		decodeData(t, statusResp, &status)
		if status.Enabled {
			t.Error("status.enabled = true after a wrong code, want false")
		}
	})

	t.Run("verify without setup is rejected", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "u@test.local", "user")
		token := e.tokenForUser(t, id, "user")

		resp := e.post(t, "/api/v1/auth/2fa/verify", token, map[string]string{"code": "123456"})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("setup is rejected once already enabled", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "u@test.local", "user")
		enable2FA(t, e.deps, id)
		token := e.tokenForUser(t, id, "user")

		resp := e.post(t, "/api/v1/auth/2fa/setup", token, nil)
		assertStatus(t, resp, http.StatusConflict)
	})

	t.Run("OIDC accounts cannot enroll", func(t *testing.T) {
		e := newTestEnv(t)
		u := &db.User{DisplayName: "OIDC User", Email: "oidc@test.local", OIDCProvider: "test-provider", OIDCSub: "sub-1", IsActive: true}
		if err := e.deps.users.Create(context.Background(), u); err != nil {
			t.Fatalf("create OIDC user: %v", err)
		}
		token := e.tokenForUser(t, u.ID, "user")

		resp := e.post(t, "/api/v1/auth/2fa/setup", token, nil)
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("disable requires the correct password", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "u@test.local", "user")
		enable2FA(t, e.deps, id)
		token := e.tokenForUser(t, id, "user")

		wrong := e.post(t, "/api/v1/auth/2fa/disable", token, map[string]string{"password": "wrong-password"})
		assertStatus(t, wrong, http.StatusBadRequest)

		ok := e.post(t, "/api/v1/auth/2fa/disable", token, map[string]string{"password": "test-password-123"})
		assertStatus(t, ok, http.StatusNoContent)

		statusResp := e.get(t, "/api/v1/auth/2fa/status", token)
		var status twoFactorStatus
		decodeData(t, statusResp, &status)
		if status.Enabled {
			t.Error("status.enabled = true after disable, want false")
		}
	})

	t.Run("recovery code is single use at login", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "u@test.local", "user")
		token := e.tokenForUser(t, id, "user")

		setupResp := e.post(t, "/api/v1/auth/2fa/setup", token, nil)
		var setup twoFactorSetup
		decodeData(t, setupResp, &setup)
		verifyResp := e.post(t, "/api/v1/auth/2fa/verify", token, map[string]string{"code": currentCode(t, setup.Secret)})
		var codes twoFactorRecoveryCodes
		decodeData(t, verifyResp, &codes)

		out := loginStepOne(t, e, "u@test.local")
		first := e.post(t, "/api/v1/auth/login/2fa", "", map[string]string{
			"challenge_token": out.ChallengeToken,
			"code":            codes.RecoveryCodes[0],
		})
		assertStatus(t, first, http.StatusOK)

		out2 := loginStepOne(t, e, "u@test.local")
		reuse := e.post(t, "/api/v1/auth/login/2fa", "", map[string]string{
			"challenge_token": out2.ChallengeToken,
			"code":            codes.RecoveryCodes[0],
		})
		assertStatus(t, reuse, http.StatusBadRequest)
	})

	t.Run("regenerate replaces the whole set", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "u@test.local", "user")
		enable2FA(t, e.deps, id)
		token := e.tokenForUser(t, id, "user")

		resp := e.post(t, "/api/v1/auth/2fa/recovery-codes/regenerate", token, map[string]string{"password": "test-password-123"})
		assertStatus(t, resp, http.StatusOK)
		var codes twoFactorRecoveryCodes
		decodeData(t, resp, &codes)
		if len(codes.RecoveryCodes) != auth.RecoveryCodeCount {
			t.Errorf("got %d recovery codes, want %d", len(codes.RecoveryCodes), auth.RecoveryCodeCount)
		}
	})
}

func TestTwoFactorAdminReset(t *testing.T) {
	t.Run("admin resets a locked-out user and password-only login works again", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "locked@test.local", "user")
		enable2FA(t, e.deps, id)
		admin := e.adminToken(t)

		resp := e.post(t, "/api/v1/users/"+id.String()+"/2fa/reset", admin, nil)
		assertStatus(t, resp, http.StatusNoContent)

		out := loginStepOne(t, e, "locked@test.local")
		if out.TwoFactorRequired {
			t.Error("two_factor_required = true after admin reset, want false")
		}
		if out.AccessToken == "" {
			t.Error("access_token is empty after admin reset")
		}
	})

	t.Run("non-admin is forbidden", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "u@test.local", "user")
		enable2FA(t, e.deps, id)
		user := e.userToken(t)

		resp := e.post(t, "/api/v1/users/"+id.String()+"/2fa/reset", user, nil)
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("unknown user is 404", func(t *testing.T) {
		e := newTestEnv(t)
		admin := e.adminToken(t)

		resp := e.post(t, "/api/v1/users/"+uuid.NewString()+"/2fa/reset", admin, nil)
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("user response exposes two_factor_enabled", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "u@test.local", "user")
		enable2FA(t, e.deps, id)
		admin := e.adminToken(t)

		resp := e.get(t, "/api/v1/users/"+id.String(), admin)
		assertStatus(t, resp, http.StatusOK)
		var out struct {
			TwoFactorEnabled bool `json:"two_factor_enabled"`
		}
		decodeData(t, resp, &out)
		if !out.TwoFactorEnabled {
			t.Error("two_factor_enabled = false, want true")
		}
	})
}
