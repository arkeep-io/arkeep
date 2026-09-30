package api

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/auth/oidctest"
	"github.com/arkeep-io/arkeep/server/internal/db"
)

// oidcFlow drives the OIDC endpoints like a browser: it keeps cookies and
// stops at every redirect so the tests can assert where it points.
type oidcFlow struct {
	e      *testEnv
	idp    *oidctest.Provider
	cfg    *db.OIDCProvider
	client *http.Client
}

func newOIDCFlow(t *testing.T, allowedGroups string) *oidcFlow {
	t.Helper()
	e := newTestEnv(t)
	idp := oidctest.New(t, "arkeep")
	cfg := &db.OIDCProvider{
		Name:          "Test IdP",
		Issuer:        idp.URL,
		ClientID:      "arkeep",
		ClientSecret:  "secret",
		Scopes:        "openid email profile",
		Enabled:       true,
		GroupsClaim:   "groups",
		AllowedGroups: allowedGroups,
	}
	if err := e.deps.oidc.Create(context.Background(), cfg); err != nil {
		t.Fatalf("creating provider: %v", err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &oidcFlow{e: e, idp: idp, cfg: cfg, client: client}
}

// callback completes the flow started at authURL (the identity provider's
// authorization URL) and returns where the callback redirects to.
func (f *oidcFlow) callback(t *testing.T, authURL string) string {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parsing authorization URL: %v", err)
	}
	resp, err := f.client.Get(f.e.URL + "/auth/oidc/callback?code=code&state=" + url.QueryEscape(u.Query().Get("state")))
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	_ = resp.Body.Close()
	assertStatus(t, resp, http.StatusFound)
	return resp.Header.Get("Location")
}

// login runs a whole sign-in and returns where it ends.
func (f *oidcFlow) login(t *testing.T) string {
	t.Helper()
	resp, err := f.client.Get(f.e.URL + "/api/v1/auth/oidc/login?provider_id=" + f.cfg.ID.String())
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = resp.Body.Close()
	assertStatus(t, resp, http.StatusFound)
	return f.callback(t, resp.Header.Get("Location"))
}

// startLink calls POST /auth/oidc/link as the given user through the flow's
// client, so the cookies it sets are kept.
func (f *oidcFlow) startLink(t *testing.T, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, f.e.URL+"/api/v1/auth/oidc/link",
		strings.NewReader(`{"provider_id":"`+f.cfg.ID.String()+`"}`))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	return resp
}

func TestOIDCCallback_Login(t *testing.T) {
	t.Run("signs in a provisioned user", func(t *testing.T) {
		f := newOIDCFlow(t, "")
		f.idp.IDTokenClaims = map[string]any{"sub": "alice", "email": "alice@example.com"}
		if loc := f.login(t); !strings.HasPrefix(loc, "/auth/callback?token=") {
			t.Errorf("redirect = %q, want the GUI callback with a token", loc)
		}
	})

	t.Run("redirects a user outside the allowed groups to the login page", func(t *testing.T) {
		f := newOIDCFlow(t, "backup")
		f.idp.IDTokenClaims = map[string]any{"sub": "alice", "email": "alice@example.com", "groups": []any{"dev"}}
		if loc := f.login(t); loc != "/login?oidc_error=access_denied" {
			t.Errorf("redirect = %q, want /login?oidc_error=access_denied", loc)
		}
	})

	t.Run("refuses to sign in as an existing account with the same email", func(t *testing.T) {
		f := newOIDCFlow(t, "")
		createDBUser(t, f.e.deps, "admin@example.com", "admin")
		f.idp.IDTokenClaims = map[string]any{"sub": "attacker", "email": "admin@example.com"}
		if loc := f.login(t); loc != "/login?oidc_error=account_exists" {
			t.Errorf("redirect = %q, want /login?oidc_error=account_exists", loc)
		}
	})

	t.Run("redirects when the session cookies are gone", func(t *testing.T) {
		f := newOIDCFlow(t, "")
		if loc := f.callback(t, "http://idp.test/authorize?state=s"); loc != "/login?oidc_error=expired" {
			t.Errorf("redirect = %q, want /login?oidc_error=expired", loc)
		}
	})
}

func TestOIDCLink(t *testing.T) {
	t.Run("requires authentication", func(t *testing.T) {
		f := newOIDCFlow(t, "")
		resp := f.startLink(t, "")
		_ = resp.Body.Close()
		assertStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("rejects an account that already uses single sign-on", func(t *testing.T) {
		f := newOIDCFlow(t, "")
		u := &db.User{Email: "sso@example.com", DisplayName: "SSO", Role: "user", IsActive: true, OIDCProvider: f.cfg.ID.String(), OIDCSub: "sso"}
		if err := f.e.deps.users.Create(context.Background(), u); err != nil {
			t.Fatalf("creating user: %v", err)
		}
		resp := f.startLink(t, f.e.tokenForUser(t, u.ID, "user"))
		_ = resp.Body.Close()
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("links the identity and removes password and two-factor", func(t *testing.T) {
		f := newOIDCFlow(t, "")
		userID := createDBUser(t, f.e.deps, "bob@example.com", "user")
		enableTwoFactor(t, f.e.deps, userID)

		resp := f.startLink(t, f.e.tokenForUser(t, userID, "user"))
		assertStatus(t, resp, http.StatusOK)
		var data struct {
			URL string `json:"url"`
		}
		decodeData(t, resp, &data)

		f.idp.IDTokenClaims = map[string]any{"sub": "bob-sub", "email": "bob@example.com"}
		if loc := f.callback(t, data.URL); loc != "/profile?sso=linked" {
			t.Fatalf("redirect = %q, want /profile?sso=linked", loc)
		}

		u, err := f.e.deps.users.GetByID(context.Background(), userID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if u.OIDCProvider != f.cfg.ID.String() || u.OIDCSub != "bob-sub" {
			t.Errorf("linked identity = %q/%q, want %s/bob-sub", u.OIDCProvider, u.OIDCSub, f.cfg.ID)
		}
		if u.Password != "" || u.TOTPSecret != "" || u.TwoFactorEnabled {
			t.Error("password or two-factor state kept, want the account SSO-only")
		}
		if n, _ := f.e.deps.recoveryCodes.CountUnused(context.Background(), userID); n != 0 {
			t.Errorf("recovery codes left = %d, want 0", n)
		}

		// From now on the identity signs in to the linked account.
		if loc := f.login(t); !strings.HasPrefix(loc, "/auth/callback?token=") {
			t.Errorf("login after link redirect = %q, want the GUI callback with a token", loc)
		}
	})

	t.Run("returns link errors to the profile page", func(t *testing.T) {
		f := newOIDCFlow(t, "backup")
		userID := createDBUser(t, f.e.deps, "erin@example.com", "user")

		resp := f.startLink(t, f.e.tokenForUser(t, userID, "user"))
		assertStatus(t, resp, http.StatusOK)
		var data struct {
			URL string `json:"url"`
		}
		decodeData(t, resp, &data)

		f.idp.IDTokenClaims = map[string]any{"sub": "erin-sub", "email": "erin@example.com"}
		if loc := f.callback(t, data.URL); loc != "/profile?oidc_error=access_denied" {
			t.Errorf("redirect = %q, want /profile?oidc_error=access_denied", loc)
		}
	})
}

// enableTwoFactor gives the user an enabled TOTP secret and one recovery code.
func enableTwoFactor(t *testing.T, deps *testDeps, userID uuid.UUID) {
	t.Helper()
	u, err := deps.users.GetByID(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	u.TOTPSecret = "JBSWY3DPEHPK3PXP"
	u.TwoFactorEnabled = true
	if err := deps.users.Update(context.Background(), u); err != nil {
		t.Fatalf("enabling two-factor: %v", err)
	}
	if err := deps.recoveryCodes.CreateBatch(context.Background(), []db.RecoveryCode{{UserID: userID, CodeHash: "hash"}}); err != nil {
		t.Fatalf("creating recovery code: %v", err)
	}
}
