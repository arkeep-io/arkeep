package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/db"
)

// TestUpdateMe_PasswordChange guards against a stolen access token being
// enough to take an account over: changing the password needs the current
// one, and it closes every other session (SEC-10).
func TestUpdateMe_PasswordChange(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	userID := createDBUser(t, e.deps, "pw@example.com", "user")
	token := e.tokenForUser(t, userID, "user")

	session := func(raw string) {
		if err := e.deps.tokens.Create(ctx, &db.RefreshToken{UserID: userID, TokenHash: auth.HashToken(raw), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatalf("create session: %v", err)
		}
	}
	session("this-session")
	session("other-session")

	patch := func(body map[string]any) *http.Response {
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPatch, e.URL+"/api/v1/users/me", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.AddCookie(&http.Cookie{Name: refreshTokenCookie, Value: "this-session"})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PATCH /users/me: %v", err)
		}
		return resp
	}

	assertStatus(t, patch(map[string]any{"password": "new-password-456"}), http.StatusBadRequest)
	assertStatus(t, patch(map[string]any{"password": "new-password-456", "current_password": "wrong-password"}), http.StatusBadRequest)
	assertPassword(t, e, userID, "test-password-123")

	assertStatus(t, patch(map[string]any{"password": "new-password-456", "current_password": "test-password-123"}), http.StatusOK)
	assertPassword(t, e, userID, "new-password-456")

	if _, err := e.deps.tokens.GetByHash(ctx, auth.HashToken("this-session")); err != nil {
		t.Errorf("the session that changed the password was revoked: %v", err)
	}
	if _, err := e.deps.tokens.GetByHash(ctx, auth.HashToken("other-session")); err == nil {
		t.Error("another session survived the password change")
	}

	// A display name change alone still needs no password.
	assertStatus(t, patch(map[string]any{"display_name": "Renamed"}), http.StatusOK)
}

func assertPassword(t *testing.T, e *testEnv, userID uuid.UUID, want string) {
	t.Helper()
	user, err := e.deps.users.GetByID(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !auth.VerifyPassword(want, string(user.Password)) {
		t.Errorf("stored password is not %q", want)
	}
}
