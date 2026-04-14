package api

import (
	"net/http"
	"testing"
)

// TestAuthHandler_Login exercises POST /api/v1/auth/login.
// Note: each login attempt that succeeds runs Argon2 verification (~100 ms).
func TestAuthHandler_Login(t *testing.T) {
	t.Run("returns 200 and access token with valid credentials", func(t *testing.T) {
		e := newTestEnv(t)
		createDBUser(t, e.deps, "admin@example.com", "admin")

		resp := e.post(t, "/api/v1/auth/login", "", map[string]string{
			"email":    "admin@example.com",
			"password": "test-password-123",
		})
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			AccessToken string `json:"access_token"`
			ExpiresIn   int    `json:"expires_in"`
		}
		decodeData(t, resp, &data)
		if data.AccessToken == "" {
			t.Error("access_token is empty")
		}
		if data.ExpiresIn <= 0 {
			t.Errorf("expires_in = %d, want > 0", data.ExpiresIn)
		}

		// Verify the issued token sets a refresh cookie.
		found := false
		for _, c := range resp.Cookies() {
			if c.Name == refreshTokenCookie {
				found = true
				if !c.HttpOnly {
					t.Error("refresh token cookie should be HttpOnly")
				}
			}
		}
		if !found {
			t.Error("response did not set refresh token cookie")
		}
	})

	t.Run("returns 401 with wrong password", func(t *testing.T) {
		e := newTestEnv(t)
		createDBUser(t, e.deps, "admin@example.com", "admin")

		resp := e.post(t, "/api/v1/auth/login", "", map[string]string{
			"email":    "admin@example.com",
			"password": "wrong-password",
		})
		assertStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("returns 401 for non-existent user", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/auth/login", "", map[string]string{
			"email":    "nobody@example.com",
			"password": "any-password",
		})
		assertStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("returns 400 when email is empty", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/auth/login", "", map[string]string{
			"email":    "",
			"password": "password123",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when password is empty", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/auth/login", "", map[string]string{
			"email":    "admin@example.com",
			"password": "",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})
}

// TestAuthHandler_Logout exercises POST /api/v1/auth/logout.
func TestAuthHandler_Logout(t *testing.T) {
	t.Run("returns 204 when authenticated", func(t *testing.T) {
		e := newTestEnv(t)
		token := e.adminToken(t)

		resp := e.postWithCookie(t, "/api/v1/auth/logout", token, nil, nil)
		assertStatus(t, resp, http.StatusNoContent)
	})

	t.Run("returns 401 when not authenticated", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/auth/logout", "", nil)
		assertStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("clears the refresh cookie on logout", func(t *testing.T) {
		e := newTestEnv(t)
		// Perform a real login to get a valid refresh cookie.
		createDBUser(t, e.deps, "user@example.com", "admin")
		loginResp := e.post(t, "/api/v1/auth/login", "", map[string]string{
			"email":    "user@example.com",
			"password": "test-password-123",
		})
		assertStatus(t, loginResp, http.StatusOK)

		var loginData struct {
			AccessToken string `json:"access_token"`
		}
		decodeData(t, loginResp, &loginData)

		var refreshCookie *http.Cookie
		for _, c := range loginResp.Cookies() {
			if c.Name == refreshTokenCookie {
				refreshCookie = c
			}
		}
		if refreshCookie == nil {
			t.Fatal("no refresh cookie set after login")
		}

		logoutResp := e.postWithCookie(t, "/api/v1/auth/logout", loginData.AccessToken, refreshCookie, nil)
		assertStatus(t, logoutResp, http.StatusNoContent)

		// The response should set the cookie with MaxAge=-1 to clear it.
		for _, c := range logoutResp.Cookies() {
			if c.Name == refreshTokenCookie && c.MaxAge == -1 {
				return // cleared
			}
		}
		t.Error("refresh token cookie was not cleared after logout")
	})
}

// TestAuthHandler_Refresh exercises POST /api/v1/auth/refresh.
func TestAuthHandler_Refresh(t *testing.T) {
	t.Run("returns 401 when no refresh cookie is present", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/auth/refresh", "", nil)
		assertStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("returns 401 with an invalid refresh token value", func(t *testing.T) {
		e := newTestEnv(t)
		cookie := &http.Cookie{
			Name:  refreshTokenCookie,
			Value: "not-a-valid-refresh-token",
		}
		resp := e.postWithCookie(t, "/api/v1/auth/refresh", "", cookie, nil)
		assertStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("returns new access token with a valid refresh cookie", func(t *testing.T) {
		e := newTestEnv(t)
		createDBUser(t, e.deps, "user@example.com", "admin")

		// Login to get a valid refresh cookie.
		loginResp := e.post(t, "/api/v1/auth/login", "", map[string]string{
			"email":    "user@example.com",
			"password": "test-password-123",
		})
		assertStatus(t, loginResp, http.StatusOK)

		var loginData struct {
			AccessToken string `json:"access_token"`
		}
		decodeData(t, loginResp, &loginData)

		var refreshCookie *http.Cookie
		for _, c := range loginResp.Cookies() {
			if c.Name == refreshTokenCookie {
				refreshCookie = c
			}
		}
		if refreshCookie == nil {
			t.Fatal("no refresh cookie after login")
		}

		refreshResp := e.postWithCookie(t, "/api/v1/auth/refresh", "", refreshCookie, nil)
		assertStatus(t, refreshResp, http.StatusOK)

		var refreshData struct {
			AccessToken string `json:"access_token"`
		}
		decodeData(t, refreshResp, &refreshData)
		if refreshData.AccessToken == "" {
			t.Error("refreshed access token is empty")
		}
		if refreshData.AccessToken == loginData.AccessToken {
			t.Error("refreshed token should differ from original")
		}
	})
}

// TestAuthHandler_ListOIDCProviders exercises GET /api/v1/auth/oidc/providers.
func TestAuthHandler_ListOIDCProviders(t *testing.T) {
	t.Run("returns empty list when no OIDC providers are configured", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/auth/oidc/providers", "")
		assertStatus(t, resp, http.StatusOK)

		var data []any
		decodeData(t, resp, &data)
		if len(data) != 0 {
			t.Errorf("expected 0 providers, got %d", len(data))
		}
	})
}
