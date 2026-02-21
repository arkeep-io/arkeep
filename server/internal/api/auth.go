package api

import (
	"errors"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/auth"
)

const (
	// refreshTokenCookie is the name of the httpOnly cookie that stores the
	// refresh token. It is never exposed in API response bodies.
	refreshTokenCookie = "arkeep_refresh_token"

	// oidcStateCookie and oidcVerifierCookie hold the OIDC state and PKCE
	// code verifier between the authorization redirect and the callback.
	// Both are short-lived (10 minutes) and httpOnly.
	oidcStateCookie    = "arkeep_oidc_state"
	oidcVerifierCookie = "arkeep_oidc_verifier"

	// oidcCookieTTL is how long the OIDC session cookies are valid.
	// Must be longer than the identity provider's authorization timeout.
	oidcCookieTTL = 10 * time.Minute
)

// AuthHandler groups all authentication-related HTTP handlers.
// It depends on AuthService as the single entry point for all auth operations.
type AuthHandler struct {
	svc    *auth.AuthService
	logger *zap.Logger
	secure bool // true in production (HTTPS), false in development
}

// NewAuthHandler creates a new AuthHandler.
// secure controls whether cookies are set with the Secure flag — set to true
// in production and false in local development over HTTP.
func NewAuthHandler(svc *auth.AuthService, logger *zap.Logger, secure bool) *AuthHandler {
	return &AuthHandler{
		svc:    svc,
		logger: logger.Named("auth_handler"),
		secure: secure,
	}
}

// -----------------------------------------------------------------------------
// Local auth
// -----------------------------------------------------------------------------

// loginRequest is the JSON body expected by POST /api/v1/auth/login.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// loginResponse is the JSON body returned on successful login.
// The refresh token is not included here — it is set as an httpOnly cookie.
type loginResponse struct {
	AccessToken string `json:"access_token"`
}

// Login handles POST /api/v1/auth/login.
// Authenticates via email/password and returns an access token in the body
// and a refresh token in an httpOnly cookie.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Email == "" || req.Password == "" {
		ErrBadRequest(w, "email and password are required")
		return
	}

	pair, err := h.svc.LoginLocal(r.Context(), auth.LoginRequest{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		// Use the same 401 for both wrong credentials and disabled accounts
		// to avoid user enumeration.
		if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrUserDisabled) {
			ErrUnauthorized(w)
			return
		}
		h.logger.Error("login failed", zap.String("email", req.Email), zap.Error(err))
		ErrInternal(w)
		return
	}

	h.setRefreshCookie(w, pair.RefreshToken, pair.RefreshTokenExpiresAt)
	Ok(w, loginResponse{AccessToken: pair.AccessToken})
}

// Logout handles POST /api/v1/auth/logout.
// Invalidates the refresh token stored in the cookie and clears it.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(refreshTokenCookie)
	if err != nil {
		// No cookie present — already logged out, treat as success.
		NoContent(w)
		return
	}

	if err := h.svc.Logout(r.Context(), cookie.Value); err != nil {
		// Log but do not expose the error — clear the cookie regardless.
		h.logger.Warn("logout error", zap.Error(err))
	}

	h.clearRefreshCookie(w)
	NoContent(w)
}

// Refresh handles POST /api/v1/auth/refresh.
// Rotates the refresh token and returns a new access token.
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(refreshTokenCookie)
	if err != nil {
		ErrUnauthorized(w)
		return
	}

	pair, err := h.svc.RefreshToken(r.Context(), cookie.Value)
	if err != nil {
		h.clearRefreshCookie(w)
		ErrUnauthorized(w)
		return
	}

	h.setRefreshCookie(w, pair.RefreshToken, pair.RefreshTokenExpiresAt)
	Ok(w, loginResponse{AccessToken: pair.AccessToken})
}

// -----------------------------------------------------------------------------
// OIDC flow
// -----------------------------------------------------------------------------

// OIDCLogin handles GET /api/v1/auth/oidc/login.
// Generates the authorization URL and redirects the user to the identity
// provider. Stores state and code verifier in short-lived httpOnly cookies
// for CSRF protection and PKCE.
func (h *AuthHandler) OIDCLogin(w http.ResponseWriter, r *http.Request) {
	redirectURL, state, codeVerifier, err := h.svc.AuthorizationURL(r.Context())
	if err != nil {
		if errors.Is(err, auth.ErrProviderNotFound) {
			ErrBadRequest(w, "OIDC provider not configured")
			return
		}
		h.logger.Error("failed to generate OIDC authorization URL", zap.Error(err))
		ErrInternal(w)
		return
	}

	expires := time.Now().Add(oidcCookieTTL)

	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    state,
		Expires:  expires,
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
	})

	http.SetCookie(w, &http.Cookie{
		Name:     oidcVerifierCookie,
		Value:    codeVerifier,
		Expires:  expires,
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
	})

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// OIDCCallback handles GET /api/v1/auth/oidc/callback.
// Completes the Authorization Code + PKCE flow, reads state and verifier
// from the session cookies, exchanges the code for tokens, and sets the
// refresh token cookie before redirecting to the frontend.
func (h *AuthHandler) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie(oidcStateCookie)
	if err != nil {
		ErrBadRequest(w, "missing OIDC state cookie")
		return
	}

	verifierCookie, err := r.Cookie(oidcVerifierCookie)
	if err != nil {
		ErrBadRequest(w, "missing OIDC verifier cookie")
		return
	}

	// Clear the OIDC session cookies — they are single-use.
	h.clearOIDCCookies(w)

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" || state == "" {
		ErrBadRequest(w, "missing code or state parameter")
		return
	}

	pair, err := h.svc.ExchangeCode(r.Context(), auth.OIDCCallbackRequest{
		Code:         code,
		State:        state,
		SessionState: stateCookie.Value,
		CodeVerifier: verifierCookie.Value,
	})
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			ErrUnauthorized(w)
			return
		}
		h.logger.Error("OIDC code exchange failed", zap.Error(err))
		ErrInternal(w)
		return
	}

	h.setRefreshCookie(w, pair.RefreshToken, pair.RefreshTokenExpiresAt)

	// Redirect to the frontend with the access token as a query parameter.
	// The frontend must immediately store it in memory and remove it from
	// the URL to avoid leaking via the browser history or referrer headers.
	http.Redirect(w, r, "/?token="+pair.AccessToken, http.StatusFound)
}

// -----------------------------------------------------------------------------
// Cookie helpers
// -----------------------------------------------------------------------------

// setRefreshCookie writes the refresh token as an httpOnly Secure cookie.
func (h *AuthHandler) setRefreshCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshTokenCookie,
		Value:    token,
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteStrictMode,
		Path:     "/api/v1/auth",
	})
}

// clearRefreshCookie expires the refresh token cookie immediately.
func (h *AuthHandler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshTokenCookie,
		Value:    "",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteStrictMode,
		Path:     "/api/v1/auth",
	})
}

// clearOIDCCookies expires both OIDC session cookies immediately.
func (h *AuthHandler) clearOIDCCookies(w http.ResponseWriter) {
	for _, name := range []string{oidcStateCookie, oidcVerifierCookie} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Expires:  time.Unix(0, 0),
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   h.secure,
			SameSite: http.SameSiteLaxMode,
			Path:     "/",
		})
	}
}