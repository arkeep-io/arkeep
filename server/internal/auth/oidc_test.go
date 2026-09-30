package auth

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"go.uber.org/zap"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/auth/oidctest"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// TestMain initialises the AES key EncryptedString fields need; its value is
// irrelevant for an in-memory test database.
func TestMain(m *testing.M) {
	if err := db.InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		panic("db.InitEncryption: " + err.Error())
	}
	os.Exit(m.Run())
}

func TestClaimGroups(t *testing.T) {
	tests := []struct {
		name  string
		claim any
		want  []string
	}{
		{"array", []any{"a", "b"}, []string{"a", "b"}},
		{"array skips non-strings", []any{"a", 1, "b"}, []string{"a", "b"}},
		{"single string", "a", []string{"a"}},
		{"object keys", map[string]any{"a": map[string]any{}, "b": true}, []string{"a", "b"}},
		{"missing", nil, nil},
		{"unsupported type", 42.0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := claimGroups(tt.claim)
			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("claimGroups() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInAnyGroup(t *testing.T) {
	tests := []struct {
		name       string
		groups     []string
		configured string
		want       bool
	}{
		{"match", []string{"ops", "dev"}, "dev", true},
		{"match with spaces", []string{"backup admins"}, " ops , backup admins ", true},
		{"no match", []string{"dev"}, "ops,admins", false},
		{"no groups", nil, "ops", false},
		{"empty entries never match", []string{""}, ",", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inAnyGroup(tt.groups, tt.configured); got != tt.want {
				t.Errorf("inAnyGroup() = %v, want %v", got, tt.want)
			}
		})
	}
}

// oidcTestEnv wires an OIDCAuthProvider to an in-memory database and a fake
// identity provider with one enabled provider configuration.
type oidcTestEnv struct {
	p     *OIDCAuthProvider
	idp   *oidctest.Provider
	cfg   *db.OIDCProvider
	users repositories.UserRepository
	repo  repositories.OIDCProviderRepository
}

func newOIDCTestEnv(t *testing.T) *oidcTestEnv {
	t.Helper()
	gdb, err := db.New(db.Config{Driver: "sqlite", DSN: ":memory:", Logger: zap.NewNop(), LogLevel: gormlogger.Silent})
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	jwtMgr, err := NewJWTManagerGenerated("arkeep-test")
	if err != nil {
		t.Fatalf("NewJWTManagerGenerated: %v", err)
	}

	idp := oidctest.New(t, "arkeep")
	repo := repositories.NewOIDCProviderRepository(gdb)
	cfg := &db.OIDCProvider{
		Name:         "Test IdP",
		Issuer:       idp.URL,
		ClientID:     "arkeep",
		ClientSecret: "secret",
		Scopes:       "openid email profile",
		Enabled:      true,
		GroupsClaim:  "groups",
	}
	if err := repo.Create(context.Background(), cfg); err != nil {
		t.Fatalf("creating provider: %v", err)
	}

	users := repositories.NewUserRepository(gdb)
	p := NewOIDCAuthProvider(repo, users, repositories.NewRefreshTokenRepository(gdb), jwtMgr, zap.NewNop())
	return &oidcTestEnv{p: p, idp: idp, cfg: cfg, users: users, repo: repo}
}

// configure updates the provider's group settings.
func (e *oidcTestEnv) configure(t *testing.T, claim, allowed, admin string) {
	t.Helper()
	e.cfg.GroupsClaim, e.cfg.AllowedGroups, e.cfg.AdminGroups = claim, allowed, admin
	if err := e.repo.Update(context.Background(), e.cfg); err != nil {
		t.Fatalf("updating provider: %v", err)
	}
}

func (e *oidcTestEnv) callback() OIDCCallbackRequest {
	return OIDCCallbackRequest{
		ProviderID:   e.cfg.ID.String(),
		CallbackURL:  "http://arkeep.test/auth/oidc/callback",
		Code:         "code",
		State:        "state",
		SessionState: "state",
		CodeVerifier: "verifier",
	}
}

func (e *oidcTestEnv) login(t *testing.T) error {
	t.Helper()
	_, err := e.p.ExchangeCode(context.Background(), e.callback())
	return err
}

func (e *oidcTestEnv) userBySub(t *testing.T, sub string) *db.User {
	t.Helper()
	u, err := e.users.GetByOIDC(context.Background(), e.cfg.ID.String(), sub)
	if err != nil {
		t.Fatalf("GetByOIDC(%q): %v", sub, err)
	}
	return u
}

func identity(sub, email string) map[string]any {
	return map[string]any{"sub": sub, "email": email, "name": sub}
}

func TestExchangeCode_ProvisionsWithoutGroupRestriction(t *testing.T) {
	e := newOIDCTestEnv(t)
	e.idp.IDTokenClaims = identity("alice", "alice@example.com")

	if err := e.login(t); err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if u := e.userBySub(t, "alice"); u.Role != "user" {
		t.Errorf("role = %q, want user", u.Role)
	}
}

func TestExchangeCode_AllowedGroups(t *testing.T) {
	tests := []struct {
		name     string
		claim    string
		idToken  map[string]any
		userInfo map[string]any
		wantErr  error
	}{
		{"member via ID token", "groups", map[string]any{"groups": []any{"dev", "backup"}}, nil, nil},
		{"member via UserInfo", "groups", nil, map[string]any{"groups": []any{"backup"}}, nil},
		{"member via object claim", "urn:zitadel:iam:org:project:roles", map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"backup": map[string]any{}}}, nil, nil},
		{"not a member", "groups", map[string]any{"groups": []any{"dev"}}, nil, ErrOIDCAccessDenied},
		{"claim missing everywhere", "groups", nil, map[string]any{}, ErrOIDCAccessDenied},
		{"claim empty", "groups", map[string]any{"groups": []any{}}, nil, ErrOIDCAccessDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newOIDCTestEnv(t)
			e.configure(t, tt.claim, "backup", "")
			e.idp.IDTokenClaims = identity("alice", "alice@example.com")
			for k, v := range tt.idToken {
				e.idp.IDTokenClaims[k] = v
			}
			e.idp.UserInfo = tt.userInfo

			err := e.login(t)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ExchangeCode error = %v, want %v", err, tt.wantErr)
			}
			// A denied login must not provision the account.
			_, lookupErr := e.users.GetByEmail(context.Background(), "alice@example.com")
			if provisioned := lookupErr == nil; provisioned != (tt.wantErr == nil) {
				t.Errorf("account provisioned = %v, want %v", provisioned, tt.wantErr == nil)
			}
		})
	}
}

func TestExchangeCode_AdminGroupsSyncRole(t *testing.T) {
	e := newOIDCTestEnv(t)
	e.configure(t, "groups", "", "admins")
	e.idp.IDTokenClaims = identity("alice", "alice@example.com")

	e.idp.IDTokenClaims["groups"] = []any{"admins"}
	if err := e.login(t); err != nil {
		t.Fatalf("first login: %v", err)
	}
	if u := e.userBySub(t, "alice"); u.Role != "admin" {
		t.Fatalf("role after first login = %q, want admin", u.Role)
	}

	// Removed from the admin group at the IdP: demoted on the next login.
	e.idp.IDTokenClaims["groups"] = []any{"dev"}
	if err := e.login(t); err != nil {
		t.Fatalf("second login: %v", err)
	}
	if u := e.userBySub(t, "alice"); u.Role != "user" {
		t.Errorf("role after second login = %q, want user", u.Role)
	}
}

func TestExchangeCode_NeverLinksByEmail(t *testing.T) {
	e := newOIDCTestEnv(t)
	local := &db.User{Email: "admin@example.com", Password: "hash", DisplayName: "Admin", Role: "admin", IsActive: true}
	if err := e.users.Create(context.Background(), local); err != nil {
		t.Fatalf("creating local user: %v", err)
	}
	e.idp.IDTokenClaims = identity("attacker", "admin@example.com")

	if err := e.login(t); !errors.Is(err, ErrOIDCAccountExists) {
		t.Fatalf("ExchangeCode error = %v, want ErrOIDCAccountExists", err)
	}
	u, err := e.users.GetByID(context.Background(), local.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if u.OIDCProvider != "" || u.OIDCSub != "" {
		t.Errorf("local account was linked to %q/%q", u.OIDCProvider, u.OIDCSub)
	}
}

func TestExchangeCode_DisabledProvider(t *testing.T) {
	e := newOIDCTestEnv(t)
	e.cfg.Enabled = false
	if err := e.repo.Update(context.Background(), e.cfg); err != nil {
		t.Fatalf("disabling provider: %v", err)
	}
	e.idp.IDTokenClaims = identity("alice", "alice@example.com")

	if err := e.login(t); !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("ExchangeCode error = %v, want ErrProviderNotFound", err)
	}
}

func TestLinkIdentity(t *testing.T) {
	newLocal := func(t *testing.T, e *oidcTestEnv, email string) *db.User {
		t.Helper()
		u := &db.User{Email: email, Password: "hash", DisplayName: email, Role: "user", IsActive: true}
		if err := e.users.Create(context.Background(), u); err != nil {
			t.Fatalf("creating local user: %v", err)
		}
		return u
	}

	t.Run("links and makes the account SSO-only", func(t *testing.T) {
		e := newOIDCTestEnv(t)
		e.configure(t, "groups", "", "admins")
		local := newLocal(t, e, "bob@example.com")
		e.idp.IDTokenClaims = identity("bob-sub", "bob@corp.example.com")
		e.idp.IDTokenClaims["groups"] = []any{"admins"}

		if _, err := e.p.LinkIdentity(context.Background(), e.callback(), local.ID); err != nil {
			t.Fatalf("LinkIdentity: %v", err)
		}
		u := e.userBySub(t, "bob-sub")
		if u.ID != local.ID {
			t.Fatalf("identity linked to %s, want %s", u.ID, local.ID)
		}
		if u.Password != "" {
			t.Error("password was kept, want it removed")
		}
		if u.Role != "admin" {
			t.Errorf("role = %q, want admin from admin_groups", u.Role)
		}
	})

	t.Run("refuses an identity linked to another account", func(t *testing.T) {
		e := newOIDCTestEnv(t)
		e.idp.IDTokenClaims = identity("carol-sub", "carol@example.com")
		if err := e.login(t); err != nil {
			t.Fatalf("provisioning carol: %v", err)
		}
		local := newLocal(t, e, "dave@example.com")

		_, err := e.p.LinkIdentity(context.Background(), e.callback(), local.ID)
		if !errors.Is(err, ErrOIDCIdentityInUse) {
			t.Fatalf("LinkIdentity error = %v, want ErrOIDCIdentityInUse", err)
		}
	})

	t.Run("enforces allowed groups", func(t *testing.T) {
		e := newOIDCTestEnv(t)
		e.configure(t, "groups", "backup", "")
		local := newLocal(t, e, "erin@example.com")
		e.idp.IDTokenClaims = identity("erin-sub", "erin@example.com")

		_, err := e.p.LinkIdentity(context.Background(), e.callback(), local.ID)
		if !errors.Is(err, ErrOIDCAccessDenied) {
			t.Fatalf("LinkIdentity error = %v, want ErrOIDCAccessDenied", err)
		}
	})
}
