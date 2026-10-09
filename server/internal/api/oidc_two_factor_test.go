package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// fakeIdP is a minimal OpenID Connect provider: discovery, JWKS and a token
// endpoint that answers every code with an ID token for sub.
type fakeIdP struct {
	*httptest.Server
	sub string
}

func newFakeIdP(t *testing.T, clientID string) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	idp := &fakeIdP{}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                idp.URL,
			"authorization_endpoint":                idp.URL + "/authorize",
			"token_endpoint":                        idp.URL + "/token",
			"jwks_uri":                              idp.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"iss": idp.URL, "aud": clientID, "sub": idp.sub,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
			"email": idp.sub + "@example.com", "email_verified": true, "name": idp.sub,
		})
		tok.Header["kid"] = "k1"
		signed, err := tok.SignedString(key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": signed,
		})
	})
	idp.Server = httptest.NewServer(mux)
	t.Cleanup(idp.Close)
	return idp
}

// oidcCallback runs the callback step of an OIDC login and returns the
// redirect it answers with, without following it.
func oidcCallback(t *testing.T, e *testEnv, providerID uuid.UUID) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.URL+"/auth/oidc/callback?code=c&state=s", nil)
	req.AddCookie(&http.Cookie{Name: oidcStateCookie, Value: "s"})
	req.AddCookie(&http.Cookie{Name: oidcVerifierCookie, Value: "v"})
	req.AddCookie(&http.Cookie{Name: oidcProviderCookie, Value: providerID.String()})
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	if resp.StatusCode != http.StatusFound {
		b, _ := io.ReadAll(resp.Body)
		t.Logf("callback status %d: %s", resp.StatusCode, b)
	}
	_ = resp.Body.Close()
	return resp
}

// TestOIDCLogin_TwoFactor guards against an OIDC login skipping the second
// factor of an account that has one (SEC-06).
func TestOIDCLogin_TwoFactor(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	idp := newFakeIdP(t, "arkeep")
	provider := &db.OIDCProvider{Name: "idp", Issuer: idp.URL, ClientID: "arkeep", ClientSecret: db.EncryptedString("s"), Enabled: true}
	if err := e.deps.oidc.Create(ctx, provider); err != nil {
		t.Fatalf("create provider: %v", err)
	}

	linked := func(sub string) uuid.UUID {
		id := createDBUser(t, e.deps, sub+"@example.com", "user")
		if err := e.deps.gdb.Model(&db.User{}).Where("id = ?", id).
			Updates(map[string]any{"oidc_provider": provider.ID.String(), "oidc_sub": sub}).Error; err != nil {
			t.Fatalf("link user: %v", err)
		}
		return id
	}

	t.Run("without two-factor the tokens are issued", func(t *testing.T) {
		linked("plain")
		idp.sub = "plain"
		resp := oidcCallback(t, e, provider.ID)
		if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/auth/callback?token=") {
			t.Errorf("Location = %q, want the token callback", loc)
		}
	})

	t.Run("with two-factor the code step is required", func(t *testing.T) {
		userID := linked("tfa")
		secret := enable2FA(t, e.deps, userID)
		idp.sub = "tfa"

		resp := oidcCallback(t, e, provider.ID)
		loc, err := url.Parse(resp.Header.Get("Location"))
		if err != nil || loc.Path != "/login" || loc.Query().Get("challenge") == "" {
			t.Fatalf("Location = %q, want /login?challenge=…", resp.Header.Get("Location"))
		}
		for _, c := range resp.Cookies() {
			if c.Name == refreshTokenCookie && c.Value != "" {
				t.Error("a refresh token was issued before the second factor")
			}
		}

		resp2 := e.post(t, "/api/v1/auth/login/2fa", "", map[string]string{
			"challenge_token": loc.Query().Get("challenge"),
			"code":            currentCode(t, secret),
		})
		assertStatus(t, resp2, http.StatusOK)
		var out loginStepOneResult
		decodeData(t, resp2, &out)
		if out.AccessToken == "" {
			t.Error("no access token after completing the second factor")
		}
	})
}
