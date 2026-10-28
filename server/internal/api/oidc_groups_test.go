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
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// newGroupsProvider registers an enabled provider backed by idp with the given
// group settings.
func newGroupsProvider(t *testing.T, e *testEnv, idp *fakeIdP, allowed, admin string) *db.OIDCProvider {
	t.Helper()
	p := &db.OIDCProvider{
		Name: "idp", Issuer: idp.URL, ClientID: "arkeep", ClientSecret: db.EncryptedString("s"), Enabled: true,
		GroupsClaim: "groups", AllowedGroups: allowed, AdminGroups: admin,
	}
	if err := e.deps.oidc.Create(context.Background(), p); err != nil {
		t.Fatalf("create provider: %v", err)
	}
	return p
}

// refreshCookie returns the refresh token cookie set by resp, failing the test
// when there is none.
func refreshCookie(t *testing.T, resp *http.Response) string {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == refreshTokenCookie && c.Value != "" {
			return c.Value
		}
	}
	t.Fatal("no refresh token cookie")
	return ""
}

// TestOIDCLogin_Groups covers the provider's allowed and admin groups (issue
// #277): denial before provisioning, the reason shown on the callback page,
// revocation of the denied account's sessions, and role mapping without ever
// demoting the last active admin. The subtests share one server and IdP,
// each with its own provider and users, since a test server is slow to start.
func TestOIDCLogin_Groups(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	idp := newFakeIdP(t, "arkeep")

	t.Run("member of an allowed group signs in", func(t *testing.T) {
		p := newGroupsProvider(t, e, idp, "arkeep-users", "")
		idp.sub, idp.groups = "alice", []string{"other", "arkeep-users"}

		resp := oidcCallback(t, e, p.ID)
		if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/auth/callback?token=") {
			t.Errorf("Location = %q, want the token callback", loc)
		}
	})

	denied := []struct {
		name   string
		groups any
	}{
		{"user in no allowed group is denied", []string{"other"}},
		{"missing groups claim is denied", nil},
	}
	for _, tt := range denied {
		t.Run(tt.name, func(t *testing.T) {
			p := newGroupsProvider(t, e, idp, "arkeep-users", "")
			idp.sub, idp.groups = "mallory", tt.groups

			resp := oidcCallback(t, e, p.ID)
			loc, err := url.Parse(resp.Header.Get("Location"))
			if err != nil || loc.Path != "/auth/callback" || !strings.Contains(loc.Query().Get("error"), "not a member of a group allowed to sign in") {
				t.Errorf("Location = %q, want the callback page with the group denial reason", resp.Header.Get("Location"))
			}
			if _, err := e.deps.users.GetByEmail(ctx, "mallory@example.com"); err != repositories.ErrNotFound {
				t.Errorf("GetByEmail error = %v, want ErrNotFound: no account is provisioned for a denied user", err)
			}
		})
	}

	t.Run("denial revokes the sessions of the linked account", func(t *testing.T) {
		p := newGroupsProvider(t, e, idp, "arkeep-users", "")
		idp.sub, idp.groups = "bob", []string{"arkeep-users"}
		raw := refreshCookie(t, oidcCallback(t, e, p.ID))

		idp.groups = []string{"other"}
		oidcCallback(t, e, p.ID)

		if _, err := e.deps.tokens.GetByHash(ctx, auth.HashToken(raw)); err != repositories.ErrNotFound {
			t.Errorf("GetByHash error = %v, want ErrNotFound: the open session must be revoked", err)
		}
	})

	t.Run("admin groups grant and remove the admin role", func(t *testing.T) {
		createDBUser(t, e.deps, "local-admin@test.local", "admin")
		p := newGroupsProvider(t, e, idp, "arkeep-users", "arkeep-admins")
		idp.sub, idp.groups = "carol", []string{"arkeep-admins"}

		oidcCallback(t, e, p.ID)
		user, err := e.deps.users.GetByOIDC(ctx, p.ID.String(), "carol")
		if err != nil {
			t.Fatalf("GetByOIDC: %v", err)
		}
		if user.Role != "admin" {
			t.Errorf("role after login in an admin group = %q, want admin", user.Role)
		}

		idp.groups = []string{"arkeep-users"}
		oidcCallback(t, e, p.ID)
		if user, err = e.deps.users.GetByID(ctx, user.ID); err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if user.Role != "user" {
			t.Errorf("role after leaving the admin group = %q, want user", user.Role)
		}
	})

	// Counting admins needs a database where dave is the only one, so this
	// subtest gets its own server.
	t.Run("the last active admin is not demoted", func(t *testing.T) {
		e := newTestEnv(t)
		p := newGroupsProvider(t, e, idp, "", "arkeep-admins")
		idp.sub, idp.groups = "dave", []string{"arkeep-admins"}
		oidcCallback(t, e, p.ID)

		idp.groups = []string{"other"}
		resp := oidcCallback(t, e, p.ID)
		if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/auth/callback?token=") {
			t.Errorf("Location = %q, want the token callback", loc)
		}
		user, err := e.deps.users.GetByOIDC(ctx, p.ID.String(), "dave")
		if err != nil {
			t.Fatalf("GetByOIDC: %v", err)
		}
		if user.Role != "admin" {
			t.Errorf("role = %q, want admin: the last active admin must keep the role", user.Role)
		}
	})
}

// TestOIDCLogin_SessionDeadline checks that an OIDC session gets the
// provider's deadline, also when it is completed through the two-factor step,
// and that a local login gets none.
func TestOIDCLogin_SessionDeadline(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	idp := newFakeIdP(t, "arkeep")
	p := &db.OIDCProvider{Name: "idp", Issuer: idp.URL, ClientID: "arkeep", ClientSecret: db.EncryptedString("s"), Enabled: true, SessionMaxHours: 3}
	if err := e.deps.oidc.Create(ctx, p); err != nil {
		t.Fatalf("create provider: %v", err)
	}

	// assertDeadline checks the session deadline of the refresh token raw.
	assertDeadline := func(t *testing.T, raw string, before, after time.Time) {
		t.Helper()
		stored, err := e.deps.tokens.GetByHash(ctx, auth.HashToken(raw))
		if err != nil {
			t.Fatalf("GetByHash: %v", err)
		}
		got := stored.SessionExpiresAt
		if got == nil || got.Before(before.Add(3*time.Hour)) || got.After(after.Add(3*time.Hour)) {
			t.Errorf("SessionExpiresAt = %v, want 3 hours after the login", got)
		}
		if !stored.ExpiresAt.Equal(*got) {
			t.Errorf("ExpiresAt = %v, want the session deadline %v", stored.ExpiresAt, *got)
		}
	}

	t.Run("oidc login", func(t *testing.T) {
		idp.sub = "erin"
		before := time.Now().Truncate(time.Second)
		resp := oidcCallback(t, e, p.ID)
		assertDeadline(t, refreshCookie(t, resp), before, time.Now())
	})

	t.Run("oidc login completed by the second factor", func(t *testing.T) {
		userID := createDBUser(t, e.deps, "frank@example.com", "user")
		if err := e.deps.gdb.Model(&db.User{}).Where("id = ?", userID).
			Updates(map[string]any{"oidc_provider": p.ID.String(), "oidc_sub": "frank"}).Error; err != nil {
			t.Fatalf("link user: %v", err)
		}
		secret := enable2FA(t, e.deps, userID)
		idp.sub = "frank"

		before := time.Now().Truncate(time.Second)
		loc, err := url.Parse(oidcCallback(t, e, p.ID).Header.Get("Location"))
		if err != nil || loc.Query().Get("challenge") == "" {
			t.Fatalf("no two-factor challenge in the callback redirect")
		}
		resp := e.post(t, "/api/v1/auth/login/2fa", "", map[string]string{
			"challenge_token": loc.Query().Get("challenge"),
			"code":            currentCode(t, secret),
		})
		assertStatus(t, resp, http.StatusOK)
		assertDeadline(t, refreshCookie(t, resp), before, time.Now())
	})

	t.Run("local login has no deadline", func(t *testing.T) {
		createDBUser(t, e.deps, "grace@test.local", "user")
		resp := e.post(t, "/api/v1/auth/login", "", map[string]string{
			"email": "grace@test.local", "password": "test-password-123",
		})
		assertStatus(t, resp, http.StatusOK)
		stored, err := e.deps.tokens.GetByHash(ctx, auth.HashToken(refreshCookie(t, resp)))
		if err != nil {
			t.Fatalf("GetByHash: %v", err)
		}
		if stored.SessionExpiresAt != nil {
			t.Errorf("SessionExpiresAt = %v, want nil", *stored.SessionExpiresAt)
		}
	})

}
