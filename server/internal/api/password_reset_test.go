package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/db"
)

// tokenFromEmail extracts the raw reset token from the ?token= query parameter
// embedded in a sent email body.
func tokenFromEmail(t *testing.T, body string) string {
	t.Helper()
	_, raw, found := strings.Cut(body, "token=")
	if !found {
		t.Fatalf("email body has no token= link:\n%s", body)
	}
	// The link is followed by \r\n; cut at the first whitespace.
	if cut := strings.IndexAny(raw, " \r\n"); cut != -1 {
		raw = raw[:cut]
	}
	tok, err := url.QueryUnescape(raw)
	if err != nil {
		t.Fatalf("tokenFromEmail: unescape: %v", err)
	}
	return tok
}

// createOIDCUser inserts an active OIDC-backed user (empty password, provider set).
func createOIDCUser(t *testing.T, deps *testDeps, email string) {
	t.Helper()
	u := &db.User{
		DisplayName:  "OIDC User",
		Email:        email,
		Role:         "user",
		IsActive:     true,
		OIDCProvider: "some-provider-id",
		OIDCSub:      "sub-123",
	}
	if err := deps.users.Create(context.Background(), u); err != nil {
		t.Fatalf("createOIDCUser: %v", err)
	}
}

// TestPasswordReset_Status exercises GET /api/v1/auth/password-reset/status.
func TestPasswordReset_Status(t *testing.T) {
	t.Run("reports smtp configured", func(t *testing.T) {
		e := newTestEnv(t)
		e.mailer.configured = true

		resp := e.get(t, "/api/v1/auth/password-reset/status", "")
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			SMTPConfigured bool `json:"smtp_configured"`
		}
		decodeData(t, resp, &data)
		if !data.SMTPConfigured {
			t.Error("smtp_configured = false, want true")
		}
	})

	t.Run("reports smtp not configured", func(t *testing.T) {
		e := newTestEnv(t)
		e.mailer.configured = false

		resp := e.get(t, "/api/v1/auth/password-reset/status", "")
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			SMTPConfigured bool `json:"smtp_configured"`
		}
		decodeData(t, resp, &data)
		if data.SMTPConfigured {
			t.Error("smtp_configured = true, want false")
		}
	})
}

// TestPasswordReset_Request exercises POST /api/v1/auth/password-reset/request.
// Every branch returns the same generic 200 so the endpoint never leaks which
// emails are registered.
func TestPasswordReset_Request(t *testing.T) {
	t.Run("local user receives an email", func(t *testing.T) {
		e := newTestEnv(t)
		createDBUser(t, e.deps, "local@example.com", "user")

		resp := e.post(t, "/api/v1/auth/password-reset/request", "", map[string]string{
			"email": "local@example.com",
		})
		assertStatus(t, resp, http.StatusOK)

		if len(e.mailer.sent) != 1 {
			t.Fatalf("sent %d emails, want 1", len(e.mailer.sent))
		}
		if got := e.mailer.sent[0].to; len(got) != 1 || got[0] != "local@example.com" {
			t.Errorf("recipient = %v, want [local@example.com]", got)
		}
	})

	t.Run("oidc user receives no email", func(t *testing.T) {
		e := newTestEnv(t)
		createOIDCUser(t, e.deps, "sso@example.com")

		resp := e.post(t, "/api/v1/auth/password-reset/request", "", map[string]string{
			"email": "sso@example.com",
		})
		assertStatus(t, resp, http.StatusOK)

		if len(e.mailer.sent) != 0 {
			t.Errorf("sent %d emails for OIDC user, want 0", len(e.mailer.sent))
		}
	})

	t.Run("unknown email sends nothing but returns 200", func(t *testing.T) {
		e := newTestEnv(t)

		resp := e.post(t, "/api/v1/auth/password-reset/request", "", map[string]string{
			"email": "nobody@example.com",
		})
		assertStatus(t, resp, http.StatusOK)

		if len(e.mailer.sent) != 0 {
			t.Errorf("sent %d emails for unknown user, want 0", len(e.mailer.sent))
		}
	})

	t.Run("returns 400 when email is empty", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/auth/password-reset/request", "", map[string]string{
			"email": "",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("link uses the configured base URL", func(t *testing.T) {
		e := newTestEnvWithBaseURL(t, "https://arkeep.example.com")
		createDBUser(t, e.deps, "based@example.com", "user")

		resp := e.post(t, "/api/v1/auth/password-reset/request", "", map[string]string{
			"email": "based@example.com",
		})
		assertStatus(t, resp, http.StatusOK)

		if len(e.mailer.sent) != 1 {
			t.Fatalf("sent %d emails, want 1", len(e.mailer.sent))
		}
		body := e.mailer.sent[0].body
		if !strings.Contains(body, "https://arkeep.example.com/auth/reset-password?token=") {
			t.Errorf("email link does not use configured base URL:\n%s", body)
		}
	})
}

// TestPasswordReset_Confirm exercises POST /api/v1/auth/password-reset/confirm.
func TestPasswordReset_Confirm(t *testing.T) {
	// requestToken drives a full request for the given email and returns the raw
	// token extracted from the sent email.
	requestToken := func(t *testing.T, e *testEnv, email string) string {
		t.Helper()
		resp := e.post(t, "/api/v1/auth/password-reset/request", "", map[string]string{"email": email})
		assertStatus(t, resp, http.StatusOK)
		if len(e.mailer.sent) == 0 {
			t.Fatal("no email sent during request")
		}
		return tokenFromEmail(t, e.mailer.sent[len(e.mailer.sent)-1].body)
	}

	t.Run("valid token sets a new password", func(t *testing.T) {
		e := newTestEnv(t)
		createDBUser(t, e.deps, "reset@example.com", "user")
		token := requestToken(t, e, "reset@example.com")

		resp := e.post(t, "/api/v1/auth/password-reset/confirm", "", map[string]string{
			"token":    token,
			"password": "brand-new-password",
		})
		assertStatus(t, resp, http.StatusOK)

		// The new password must now authenticate.
		loginResp := e.post(t, "/api/v1/auth/login", "", map[string]string{
			"email":    "reset@example.com",
			"password": "brand-new-password",
		})
		assertStatus(t, loginResp, http.StatusOK)
	})

	t.Run("token cannot be reused", func(t *testing.T) {
		e := newTestEnv(t)
		createDBUser(t, e.deps, "reuse@example.com", "user")
		token := requestToken(t, e, "reuse@example.com")

		first := e.post(t, "/api/v1/auth/password-reset/confirm", "", map[string]string{
			"token":    token,
			"password": "first-new-password",
		})
		assertStatus(t, first, http.StatusOK)

		second := e.post(t, "/api/v1/auth/password-reset/confirm", "", map[string]string{
			"token":    token,
			"password": "second-new-password",
		})
		assertStatus(t, second, http.StatusBadRequest)
	})

	t.Run("invalid token returns 400", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/auth/password-reset/confirm", "", map[string]string{
			"token":    "not-a-real-token",
			"password": "some-new-password",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("short password returns 400", func(t *testing.T) {
		e := newTestEnv(t)
		createDBUser(t, e.deps, "short@example.com", "user")
		token := requestToken(t, e, "short@example.com")

		resp := e.post(t, "/api/v1/auth/password-reset/confirm", "", map[string]string{
			"token":    token,
			"password": "short",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("expired token returns 400", func(t *testing.T) {
		e := newTestEnv(t)
		userID := createDBUser(t, e.deps, "expired@example.com", "user")

		// Insert an already-expired token directly.
		raw := "expired-raw-token-value"
		if err := e.deps.resets.Create(context.Background(), &db.PasswordResetToken{
			UserID:    userID,
			TokenHash: auth.HashToken(raw),
			ExpiresAt: time.Now().Add(-time.Hour),
		}); err != nil {
			t.Fatalf("seed expired token: %v", err)
		}

		resp := e.post(t, "/api/v1/auth/password-reset/confirm", "", map[string]string{
			"token":    raw,
			"password": "some-new-password",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})
}
