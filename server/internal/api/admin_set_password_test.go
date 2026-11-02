package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// setPassword posts to the admin Set password endpoint.
func setPassword(t *testing.T, e *testEnv, token string, userID uuid.UUID, password string, mustChange bool) *http.Response {
	t.Helper()
	return e.post(t, "/api/v1/users/"+userID.String()+"/password", token, map[string]any{
		"password":    password,
		"must_change": mustChange,
	})
}

// markMustChange flags an existing user, as Set password with must_change does.
func markMustChange(t *testing.T, e *testEnv, userID uuid.UUID) {
	t.Helper()
	user, err := e.deps.users.GetByID(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	user.MustChangePassword = true
	if err := e.deps.users.Update(context.Background(), user); err != nil {
		t.Fatalf("Update: %v", err)
	}
}

type passwordChangeStep struct {
	AccessToken            string `json:"access_token"`
	TwoFactorRequired      bool   `json:"two_factor_required"`
	PasswordChangeRequired bool   `json:"password_change_required"`
	ChallengeToken         string `json:"challenge_token"`
}

func decodeStep(t *testing.T, resp *http.Response) passwordChangeStep {
	t.Helper()
	assertStatus(t, resp, http.StatusOK)
	var out passwordChangeStep
	decodeData(t, resp, &out)
	return out
}

func login(t *testing.T, e *testEnv, email, password string) *http.Response {
	t.Helper()
	return e.post(t, "/api/v1/auth/login", "", map[string]string{"email": email, "password": password})
}

func changeAtLogin(t *testing.T, e *testEnv, challenge, password string) *http.Response {
	t.Helper()
	return e.post(t, "/api/v1/auth/login/password", "", map[string]string{"challenge_token": challenge, "password": password})
}

func TestUserHandler_SetPassword(t *testing.T) {
	t.Run("sets the password, closes every session and is audited", func(t *testing.T) {
		e := newTestEnv(t)
		ctx := context.Background()
		id := createDBUser(t, e.deps, "forgot@test.local", "user")
		if err := e.deps.tokens.Create(ctx, &db.RefreshToken{UserID: id, TokenHash: auth.HashToken("old-session"), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatalf("create session: %v", err)
		}

		resp := setPassword(t, e, e.adminToken(t), id, "temporary-pass-1", true)
		assertStatus(t, resp, http.StatusOK)
		var out struct {
			MustChangePassword bool `json:"must_change_password"`
		}
		decodeData(t, resp, &out)
		if !out.MustChangePassword {
			t.Error("must_change_password = false in the response, want true")
		}

		assertPassword(t, e, id, "temporary-pass-1")
		if _, err := e.deps.tokens.GetByHash(ctx, auth.HashToken("old-session")); err == nil {
			t.Error("a session survived Set password")
		}
		entries, _, err := e.deps.audit.List(ctx, repositories.AuditFilter{Action: "user.password_set"}, repositories.ListOptions{Limit: 10})
		if err != nil || len(entries) != 1 {
			t.Fatalf("audit user.password_set = %d entries, %v; want 1", len(entries), err)
		}
	})

	t.Run("without must_change the user signs in with the new password", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "plain@test.local", "user")
		assertStatus(t, setPassword(t, e, e.adminToken(t), id, "new-password-99", false), http.StatusOK)

		step := decodeStep(t, login(t, e, "plain@test.local", "new-password-99"))
		if step.AccessToken == "" || step.PasswordChangeRequired {
			t.Errorf("login = %+v, want a session", step)
		}
	})

	t.Run("refused", func(t *testing.T) {
		e := newTestEnv(t)
		ctx := context.Background()
		id := createDBUser(t, e.deps, "target@test.local", "user")
		admin := e.adminToken(t)

		// Weak password.
		assertStatus(t, setPassword(t, e, admin, id, "short", true), http.StatusBadRequest)
		// Unknown user.
		assertStatus(t, setPassword(t, e, admin, uuid.New(), "temporary-pass-1", true), http.StatusNotFound)
		// Your own account: Profile asks for the current password instead.
		selfID := createDBUser(t, e.deps, "self@test.local", "admin")
		assertStatus(t, setPassword(t, e, e.tokenForUser(t, selfID, "admin"), selfID, "temporary-pass-1", true), http.StatusBadRequest)
		// An OIDC account: its password belongs to the identity provider.
		oidcUser, err := e.deps.users.GetByID(ctx, id)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		oidcUser.OIDCProvider = uuid.NewString()
		oidcUser.OIDCSub = "sub-1"
		if err := e.deps.users.Update(ctx, oidcUser); err != nil {
			t.Fatalf("Update: %v", err)
		}
		assertStatus(t, setPassword(t, e, admin, id, "temporary-pass-1", true), http.StatusBadRequest)
		// Not an admin.
		assertStatus(t, setPassword(t, e, e.userToken(t), id, "temporary-pass-1", true), http.StatusForbidden)

		assertPassword(t, e, selfID, "test-password-123")
	})

	t.Run("PATCH /users/{id} no longer takes a password", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "patch@test.local", "user")
		resp := e.patch(t, "/api/v1/users/"+id.String(), e.adminToken(t), map[string]any{"password": "sneaky-password-1"})
		assertStatus(t, resp, http.StatusBadRequest)
		assertPassword(t, e, id, "test-password-123")
	})
}

func TestLogin_PasswordChangeRequired(t *testing.T) {
	t.Run("no session until a new password is set", func(t *testing.T) {
		e := newTestEnv(t)
		ctx := context.Background()
		id := createDBUser(t, e.deps, "temp@test.local", "user")
		markMustChange(t, e, id)

		step := decodeStep(t, login(t, e, "temp@test.local", "test-password-123"))
		if !step.PasswordChangeRequired || step.ChallengeToken == "" {
			t.Fatalf("login = %+v, want a password change challenge", step)
		}
		if step.AccessToken != "" {
			t.Fatal("an access token was issued before the password change")
		}
		user, _ := e.deps.users.GetByID(ctx, id)
		if user.LastLoginAt != nil {
			t.Error("last_login_at was stamped before the password change")
		}

		// The new password must differ from the temporary one, and the
		// challenge survives that refusal.
		assertStatus(t, changeAtLogin(t, e, step.ChallengeToken, "test-password-123"), http.StatusBadRequest)
		assertStatus(t, changeAtLogin(t, e, step.ChallengeToken, "short"), http.StatusBadRequest)

		done := decodeStep(t, changeAtLogin(t, e, step.ChallengeToken, "my-own-password-1"))
		if done.AccessToken == "" {
			t.Fatal("no access token after the password change")
		}
		assertPassword(t, e, id, "my-own-password-1")
		user, _ = e.deps.users.GetByID(ctx, id)
		if user.MustChangePassword {
			t.Error("must_change_password still set after the change")
		}

		// Single use.
		assertStatus(t, changeAtLogin(t, e, step.ChallengeToken, "another-password-2"), http.StatusBadRequest)
		assertPassword(t, e, id, "my-own-password-1")
	})

	t.Run("unknown or expired challenge", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "temp@test.local", "user")
		markMustChange(t, e, id)

		assertStatus(t, changeAtLogin(t, e, "not-a-token", "my-own-password-1"), http.StatusBadRequest)

		raw := "expired-change-token"
		if err := e.deps.passwordChanges.Create(context.Background(), &db.PasswordChangeChallenge{
			UserID: id, TokenHash: auth.HashToken(raw), ExpiresAt: time.Now().Add(-time.Minute),
		}); err != nil {
			t.Fatalf("seed challenge: %v", err)
		}
		assertStatus(t, changeAtLogin(t, e, raw, "my-own-password-1"), http.StatusBadRequest)
		assertPassword(t, e, id, "test-password-123")
	})

	t.Run("two-factor first, then the new password", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "tfa@test.local", "user")
		secret := enable2FA(t, e.deps, id)
		markMustChange(t, e, id)

		first := decodeStep(t, login(t, e, "tfa@test.local", "test-password-123"))
		if !first.TwoFactorRequired || first.PasswordChangeRequired {
			t.Fatalf("login = %+v, want the two-factor step first", first)
		}
		// A two-factor challenge cannot complete the password step.
		assertStatus(t, changeAtLogin(t, e, first.ChallengeToken, "my-own-password-1"), http.StatusBadRequest)

		second := decodeStep(t, e.post(t, "/api/v1/auth/login/2fa", "", map[string]string{
			"challenge_token": first.ChallengeToken, "code": currentCode(t, secret),
		}))
		if !second.PasswordChangeRequired || second.AccessToken != "" {
			t.Fatalf("2fa step = %+v, want a password change challenge and no session", second)
		}
		// A password change challenge cannot complete the two-factor step.
		resp := e.post(t, "/api/v1/auth/login/2fa", "", map[string]string{
			"challenge_token": second.ChallengeToken, "code": currentCode(t, secret),
		})
		assertStatus(t, resp, http.StatusBadRequest)

		done := decodeStep(t, changeAtLogin(t, e, second.ChallengeToken, "my-own-password-1"))
		if done.AccessToken == "" {
			t.Fatal("no access token after the password change")
		}
	})

	t.Run("an inactive account cannot complete the change", func(t *testing.T) {
		e := newTestEnv(t)
		ctx := context.Background()
		id := createDBUser(t, e.deps, "temp@test.local", "user")
		markMustChange(t, e, id)
		step := decodeStep(t, login(t, e, "temp@test.local", "test-password-123"))

		user, _ := e.deps.users.GetByID(ctx, id)
		user.IsActive = false
		if err := e.deps.users.Update(ctx, user); err != nil {
			t.Fatalf("Update: %v", err)
		}
		assertStatus(t, changeAtLogin(t, e, step.ChallengeToken, "my-own-password-1"), http.StatusUnauthorized)
		assertPassword(t, e, id, "test-password-123")
	})

	t.Run("a password reset by email also clears the flag", func(t *testing.T) {
		e := newTestEnvWithBaseURL(t, "https://arkeep.example.com")
		ctx := context.Background()
		id := createDBUser(t, e.deps, "temp@test.local", "user")
		markMustChange(t, e, id)

		raw := "reset-raw-token"
		if err := e.deps.resets.Create(ctx, &db.PasswordResetToken{UserID: id, TokenHash: auth.HashToken(raw), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatalf("seed reset token: %v", err)
		}
		resp := e.post(t, "/api/v1/auth/password-reset/confirm", "", map[string]string{"token": raw, "password": "chosen-password-1"})
		assertStatus(t, resp, http.StatusOK)

		user, _ := e.deps.users.GetByID(ctx, id)
		if user.MustChangePassword {
			t.Error("must_change_password still set after a password reset")
		}
	})
}
