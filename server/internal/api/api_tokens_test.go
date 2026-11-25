package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// createAPIToken creates an API token through the API with the session of
// userID and returns the created token.
func createAPIToken(t *testing.T, e *testEnv, userID uuid.UUID, role string, body map[string]any) createdAPITokenResponse {
	t.Helper()
	resp := e.post(t, "/api/v1/users/me/api-tokens", e.tokenForUser(t, userID, role), body)
	if resp.StatusCode != http.StatusCreated {
		assertStatus(t, resp, http.StatusCreated)
		t.FailNow()
	}
	var out createdAPITokenResponse
	decodeData(t, resp, &out)
	return out
}

// assertErrorCode checks the status and the error code of a response.
func assertErrorCode(t *testing.T, resp *http.Response, status int, code string) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != status {
		assertStatus(t, resp, status)
		return
	}
	var env struct {
		Error errorResponse `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != code {
		t.Errorf("error code = %q, want %q", env.Error.Code, code)
	}
}

func TestAPITokens_CreateUseListRevoke(t *testing.T) {
	e := newTestEnv(t)
	userID := createDBUser(t, e.deps, "reader@test.local", "user")
	session := e.tokenForUser(t, userID, "user")

	created := createAPIToken(t, e, userID, "user", map[string]any{"name": " Home Assistant "})
	if !strings.HasPrefix(created.Token, "ark_") || len(created.Token) < 40 {
		t.Fatalf("token = %q, want ark_ and 256 bits", created.Token)
	}
	if created.Name != "Home Assistant" || created.Scope != "read" || created.ExpiresAt != nil {
		t.Errorf("created = %+v, want the trimmed name, scope read, no expiry", created.apiTokenResponse)
	}
	if !strings.HasPrefix(created.Token, created.Hint) || len(created.Hint) != len("ark_")+4 {
		t.Errorf("hint = %q, want the first characters of %q", created.Hint, created.Token)
	}

	// Only the hash is stored.
	stored, err := e.deps.apiTokens.ListByUser(context.Background(), userID)
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored tokens = %v, %v", stored, err)
	}
	if strings.Contains(stored[0].TokenHash, created.Token) || stored[0].TokenHash == created.Token {
		t.Error("the raw token is stored")
	}

	// The token reads the API.
	for _, path := range []string{"/api/v1/policies", "/api/v1/jobs", "/api/v1/agents", "/api/v1/users/me", "/api/v1/dashboard"} {
		resp := e.get(t, path, created.Token)
		_ = resp.Body.Close()
		assertStatus(t, resp, http.StatusOK)
	}

	// The listing never shows the token, and shows its last use.
	resp := e.get(t, "/api/v1/users/me/api-tokens", session)
	assertStatus(t, resp, http.StatusOK)
	var listed []map[string]any
	decodeData(t, resp, &listed)
	if len(listed) != 1 {
		t.Fatalf("listed %d tokens, want 1", len(listed))
	}
	if _, ok := listed[0]["token"]; ok {
		t.Error("the listing returns the token")
	}
	if listed[0]["last_used_at"] == nil {
		t.Error("last_used_at not recorded")
	}

	// Revoked, it no longer works.
	resp = e.del(t, "/api/v1/users/me/api-tokens/"+created.ID, session)
	_ = resp.Body.Close()
	assertStatus(t, resp, http.StatusNoContent)
	resp = e.get(t, "/api/v1/policies", created.Token)
	_ = resp.Body.Close()
	assertStatus(t, resp, http.StatusUnauthorized)

	// Both operations are in the audit log.
	entries, _, err := e.deps.audit.List(context.Background(), repositories.AuditFilter{}, repositories.ListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, entry := range entries {
		actions = append(actions, entry.Action)
	}
	for _, want := range []string{"api_token.create", "api_token.revoke"} {
		if !strings.Contains(strings.Join(actions, ","), want) {
			t.Errorf("audit actions = %v, want %s", actions, want)
		}
	}
}

// TestAPITokens_ReadOnly: a token never writes, and never reads more than the
// "user" role, even when its owner is an admin.
func TestAPITokens_ReadOnly(t *testing.T) {
	e := newTestEnv(t)
	adminID := createDBUser(t, e.deps, "admin@test.local", "admin")
	token := createAPIToken(t, e, adminID, "admin", map[string]any{"name": "ha"}).Token

	for _, rt := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/policies"},
		{http.MethodPost, "/api/v1/policies/00000000-0000-0000-0000-000000000001/trigger"},
		{http.MethodPatch, "/api/v1/notifications/read-all"},
		{http.MethodPatch, "/api/v1/users/me"},
		{http.MethodPost, "/api/v1/auth/2fa/setup"},
		{http.MethodDelete, "/api/v1/snapshots/00000000-0000-0000-0000-000000000001"},
	} {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			assertErrorCode(t, e.doJSON(t, rt.method, rt.path, token, map[string]any{}), http.StatusForbidden, "api_token_read_only")
		})
	}

	// Admin-only reads are closed to an admin's token.
	for _, path := range []string{"/api/v1/users", "/api/v1/audit", "/api/v1/settings/smtp", "/api/v1/agents/00000000-0000-0000-0000-000000000001/usage"} {
		t.Run("GET "+path, func(t *testing.T) {
			resp := e.get(t, path, token)
			_ = resp.Body.Close()
			assertStatus(t, resp, http.StatusForbidden)
		})
	}
}

// TestAPITokens_CannotManageTokens: a token can neither list, create nor
// revoke tokens, its own included.
func TestAPITokens_CannotManageTokens(t *testing.T) {
	e := newTestEnv(t)
	adminID := createDBUser(t, e.deps, "admin@test.local", "admin")
	created := createAPIToken(t, e, adminID, "admin", map[string]any{"name": "ha"})

	assertErrorCode(t, e.get(t, "/api/v1/users/me/api-tokens", created.Token), http.StatusForbidden, "api_token_forbidden")
	assertErrorCode(t, e.get(t, "/api/v1/api-tokens", created.Token), http.StatusForbidden, "forbidden")
	assertErrorCode(t, e.post(t, "/api/v1/users/me/api-tokens", created.Token, map[string]any{"name": "x"}), http.StatusForbidden, "api_token_read_only")
	assertErrorCode(t, e.del(t, "/api/v1/users/me/api-tokens/"+created.ID, created.Token), http.StatusForbidden, "api_token_read_only")
}

func TestAPITokens_Validation(t *testing.T) {
	e := newTestEnv(t)
	userID := createDBUser(t, e.deps, "reader@test.local", "user")
	session := e.tokenForUser(t, userID, "user")

	for name, body := range map[string]map[string]any{
		"no name":       {"name": "  "},
		"long name":     {"name": strings.Repeat("é", 101)},
		"zero days":     {"name": "x", "expires_in_days": 0},
		"too many days": {"name": "x", "expires_in_days": 3651},
		"negative days": {"name": "x", "expires_in_days": -1},
		"days and date": {"name": "x", "expires_in_days": 30, "expires_at": time.Now().Add(48 * time.Hour).Format(time.RFC3339)},
		"invalid date":  {"name": "x", "expires_at": "2030-01-01"},
		"past date":     {"name": "x", "expires_at": time.Now().Add(-time.Minute).Format(time.RFC3339)},
		"date too far":  {"name": "x", "expires_at": time.Now().Add(3651 * 24 * time.Hour).Format(time.RFC3339)},
	} {
		t.Run(name, func(t *testing.T) {
			resp := e.post(t, "/api/v1/users/me/api-tokens", session, body)
			_ = resp.Body.Close()
			assertStatus(t, resp, http.StatusUnprocessableEntity)
		})
	}

	created := createAPIToken(t, e, userID, "user", map[string]any{"name": "x", "expires_in_days": 30})
	if created.ExpiresAt == nil {
		t.Fatal("expires_at not set")
	}
	expires, err := time.Parse(time.RFC3339, *created.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(expires); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Errorf("expires in %v, want 30 days", d)
	}

	// A chosen date and time is kept as given, in UTC.
	want := time.Now().Add(10 * 24 * time.Hour).Truncate(time.Second).In(time.FixedZone("CET", 3600))
	created = createAPIToken(t, e, userID, "user", map[string]any{"name": "y", "expires_at": want.Format(time.RFC3339)})
	if created.ExpiresAt == nil {
		t.Fatal("expires_at not set")
	}
	got, err := time.Parse(time.RFC3339, *created.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Errorf("expires_at = %v, want %v", got, want)
	}
}

func TestAPITokens_LimitPerUser(t *testing.T) {
	e := newTestEnv(t)
	userID := createDBUser(t, e.deps, "reader@test.local", "user")
	for range apiTokenMaxPerUser {
		createAPIToken(t, e, userID, "user", map[string]any{"name": "x"})
	}
	resp := e.post(t, "/api/v1/users/me/api-tokens", e.tokenForUser(t, userID, "user"), map[string]any{"name": "x"})
	_ = resp.Body.Close()
	assertStatus(t, resp, http.StatusConflict)
}

// TestAPITokens_Refused covers the tokens that must stop working: expired,
// owner disabled, owner deleted, unknown.
func TestAPITokens_Refused(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)

	unknown := "ark_" + strings.Repeat("A", 43)
	resp := e.get(t, "/api/v1/policies", unknown)
	_ = resp.Body.Close()
	assertStatus(t, resp, http.StatusUnauthorized)

	t.Run("expired", func(t *testing.T) {
		userID := createDBUser(t, e.deps, "expired@test.local", "user")
		created := createAPIToken(t, e, userID, "user", map[string]any{"name": "x", "expires_in_days": 1})
		past := time.Now().Add(-time.Minute).UTC()
		if err := e.deps.gdb.Model(&db.APIToken{}).Where("id = ?", created.ID).Update("expires_at", past).Error; err != nil {
			t.Fatal(err)
		}
		resp := e.get(t, "/api/v1/policies", created.Token)
		_ = resp.Body.Close()
		assertStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("owner disabled", func(t *testing.T) {
		userID := createDBUser(t, e.deps, "disabled@test.local", "user")
		created := createAPIToken(t, e, userID, "user", map[string]any{"name": "x"})
		user, err := e.deps.users.GetByID(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		user.IsActive = false
		if err := e.deps.users.Update(ctx, user); err != nil {
			t.Fatal(err)
		}
		resp := e.get(t, "/api/v1/policies", created.Token)
		_ = resp.Body.Close()
		assertStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("owner deleted", func(t *testing.T) {
		userID := createDBUser(t, e.deps, "deleted@test.local", "user")
		created := createAPIToken(t, e, userID, "user", map[string]any{"name": "x"})
		if err := e.deps.users.Delete(ctx, userID); err != nil {
			t.Fatal(err)
		}
		resp := e.get(t, "/api/v1/policies", created.Token)
		_ = resp.Body.Close()
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

// TestAPITokens_OtherUsers: a user cannot see or revoke another user's token;
// an admin lists and revokes everyone's.
func TestAPITokens_OtherUsers(t *testing.T) {
	e := newTestEnv(t)
	aliceID := createDBUser(t, e.deps, "alice@test.local", "user")
	bobID := createDBUser(t, e.deps, "bob@test.local", "user")
	adminID := createDBUser(t, e.deps, "admin@test.local", "admin")
	alice := createAPIToken(t, e, aliceID, "user", map[string]any{"name": "alice-ha"})

	bob := e.tokenForUser(t, bobID, "user")
	resp := e.get(t, "/api/v1/users/me/api-tokens", bob)
	var bobs []apiTokenResponse
	decodeData(t, resp, &bobs)
	if len(bobs) != 0 {
		t.Errorf("bob sees %d tokens, want 0", len(bobs))
	}
	resp = e.del(t, "/api/v1/users/me/api-tokens/"+alice.ID, bob)
	_ = resp.Body.Close()
	assertStatus(t, resp, http.StatusNotFound)
	resp = e.get(t, "/api/v1/api-tokens", bob)
	_ = resp.Body.Close()
	assertStatus(t, resp, http.StatusForbidden)

	admin := e.tokenForUser(t, adminID, "admin")
	resp = e.get(t, "/api/v1/api-tokens", admin)
	var all []apiTokenResponse
	decodeData(t, resp, &all)
	if len(all) != 1 || all[0].UserEmail != "alice@test.local" || all[0].UserID != aliceID.String() || all[0].Name != "alice-ha" {
		t.Fatalf("admin listing = %+v, want alice's token with her email", all)
	}
	resp = e.del(t, "/api/v1/api-tokens/"+alice.ID, admin)
	_ = resp.Body.Close()
	assertStatus(t, resp, http.StatusNoContent)
	resp = e.get(t, "/api/v1/policies", alice.Token)
	_ = resp.Body.Close()
	assertStatus(t, resp, http.StatusUnauthorized)
}

// createDBAPIToken stores an API token for userID and returns the raw token.
func createDBAPIToken(t *testing.T, deps *testDeps, userID uuid.UUID) string {
	t.Helper()
	raw, hash, hint, err := auth.GenerateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	token := &db.APIToken{UserID: userID, Name: "x", TokenHash: hash, Hint: hint, Scope: auth.APITokenScopeRead}
	if err := deps.apiTokens.Create(context.Background(), token); err != nil {
		t.Fatalf("create api token: %v", err)
	}
	return raw
}

// assertAPITokenCount checks how many API tokens userID holds.
func assertAPITokenCount(t *testing.T, deps *testDeps, userID uuid.UUID, want int) {
	t.Helper()
	tokens, err := deps.apiTokens.ListByUser(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != want {
		t.Errorf("user holds %d API tokens, want %d", len(tokens), want)
	}
}

// TestAPITokens_RevokedWhenAccountIsReset: the events that end every session
// because the account may be in someone else's hands also revoke its API
// tokens, so a token created with a stolen session does not outlive the
// clean-up. A password change from the profile keeps them, like the session
// that made it.
func TestAPITokens_RevokedWhenAccountIsReset(t *testing.T) {
	ctx := context.Background()

	t.Run("admin sets the password", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "target@test.local", "user")
		createDBAPIToken(t, e.deps, id)
		assertStatus(t, setPassword(t, e, e.adminToken(t), id, "temporary-pass-1", true), http.StatusOK)
		assertAPITokenCount(t, e.deps, id, 0)
	})

	t.Run("admin resets two-factor", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "locked@test.local", "user")
		enable2FA(t, e.deps, id)
		createDBAPIToken(t, e.deps, id)
		resp := e.post(t, "/api/v1/users/"+id.String()+"/2fa/reset", e.adminToken(t), nil)
		_ = resp.Body.Close()
		assertStatus(t, resp, http.StatusNoContent)
		assertAPITokenCount(t, e.deps, id, 0)
	})

	t.Run("password reset by email", func(t *testing.T) {
		e := newTestEnvWithBaseURL(t, testBaseURL)
		id := createDBUser(t, e.deps, "reset@example.com", "user")
		createDBAPIToken(t, e.deps, id)
		resp := e.post(t, "/api/v1/auth/password-reset/request", "", map[string]string{"email": "reset@example.com"})
		_ = resp.Body.Close()
		assertStatus(t, resp, http.StatusOK)
		if len(e.mailer.sent) == 0 {
			t.Fatal("no email sent")
		}
		resp = e.post(t, "/api/v1/auth/password-reset/confirm", "", map[string]string{
			"token":    tokenFromEmail(t, e.mailer.sent[len(e.mailer.sent)-1].body),
			"password": "brand-new-password",
		})
		_ = resp.Body.Close()
		assertStatus(t, resp, http.StatusOK)
		assertAPITokenCount(t, e.deps, id, 0)
	})

	t.Run("OIDC login denied by the groups", func(t *testing.T) {
		e := newTestEnv(t)
		idp := newFakeIdP(t, "arkeep")
		p := newGroupsProvider(t, e, idp, "arkeep-users", "")
		idp.sub, idp.groups = "bob", []string{"arkeep-users"}
		oidcCallback(t, e, p.ID)
		user, err := e.deps.users.GetByOIDC(ctx, p.ID.String(), "bob")
		if err != nil {
			t.Fatalf("GetByOIDC: %v", err)
		}
		createDBAPIToken(t, e.deps, user.ID)

		idp.groups = []string{"other"}
		oidcCallback(t, e, p.ID)
		assertAPITokenCount(t, e.deps, user.ID, 0)
	})

	t.Run("password change from the profile keeps them", func(t *testing.T) {
		e := newTestEnv(t)
		id := createDBUser(t, e.deps, "self@test.local", "user")
		raw := createDBAPIToken(t, e.deps, id)
		resp := e.patch(t, "/api/v1/users/me", e.tokenForUser(t, id, "user"), map[string]any{
			"password": "new-password-456", "current_password": "test-password-123",
		})
		_ = resp.Body.Close()
		assertStatus(t, resp, http.StatusOK)
		resp = e.get(t, "/api/v1/policies", raw)
		_ = resp.Body.Close()
		assertStatus(t, resp, http.StatusOK)
	})
}
