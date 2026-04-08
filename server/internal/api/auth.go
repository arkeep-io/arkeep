package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/google/uuid"
)

const (
	// refreshTokenCookie is the name of the httpOnly cookie that stores the
	// refresh token. It is never exposed in API response bodies.
	refreshTokenCookie = "arkeep_refresh_token"

	// oidcStateCookie, oidcVerifierCookie, oidcProviderCookie hold the OIDC
	// session data between the authorization redirect and the callback.
	// All are short-lived (10 minutes) and httpOnly.
	oidcStateCookie    = "arkeep_oidc_state"
	oidcVerifierCookie = "arkeep_oidc_verifier"
	oidcProviderCookie = "arkeep_oidc_provider"

	// oidcCookieTTL is how long the OIDC session cookies are valid.
	oidcCookieTTL = 10 * time.Minute
)

// AuthHandler groups all authentication-related HTTP handlers.
type AuthHandler struct {
	svc    *auth.AuthService
	logger *zap.Logger
	secure bool // true in production (HTTPS), false in development
}

// NewAuthHandler creates a new AuthHandler.
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

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// Login handles POST /api/v1/auth/login.
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
		if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrUserDisabled) {
			ErrUnauthorized(w)
			return
		}
		h.logger.Error("login failed", zap.String("email", req.Email), zap.Error(err))
		ErrInternal(w)
		return
	}

	h.setRefreshCookie(w, pair.RefreshToken, pair.RefreshTokenExpiresAt)
	Ok(w, loginResponse{
		AccessToken: pair.AccessToken,
		ExpiresIn:   int(time.Until(pair.AccessTokenExpiresAt).Seconds()),
	})
}

// Logout handles POST /api/v1/auth/logout.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	// Revoke the current access token immediately so it cannot be reused
	// within its remaining TTL window, even from another device or tab.
	if claims := claimsFromCtx(r.Context()); claims != nil && claims.ID != "" {
		h.svc.RevokeAccessToken(claims.ID, claims.ExpiresAt.Time)
	}

	cookie, err := r.Cookie(refreshTokenCookie)
	if err != nil {
		NoContent(w)
		return
	}

	if err := h.svc.Logout(r.Context(), cookie.Value); err != nil {
		h.logger.Warn("logout error", zap.Error(err))
	}

	h.clearRefreshCookie(w)
	NoContent(w)
}

// Refresh handles POST /api/v1/auth/refresh.
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
	Ok(w, loginResponse{
		AccessToken: pair.AccessToken,
		ExpiresIn:   int(time.Until(pair.AccessTokenExpiresAt).Seconds()),
	})
}

// -----------------------------------------------------------------------------
// OIDC flow
// -----------------------------------------------------------------------------

// oidcProviderSummary is the public shape returned by ListOIDCProviders.
// Only id and name are exposed — no credentials or configuration details.
type oidcProviderSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListOIDCProviders handles GET /api/v1/auth/oidc/providers (public).
// Returns the list of enabled providers so the login page can render one
// SSO button per provider. Only id and name are returned.
func (h *AuthHandler) ListOIDCProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := h.svc.ListEnabledProviders(r.Context())
	if err != nil {
		h.logger.Error("failed to list enabled OIDC providers", zap.Error(err))
		ErrInternal(w)
		return
	}

	summaries := make([]oidcProviderSummary, len(providers))
	for i, p := range providers {
		summaries[i] = oidcProviderSummary{ID: p.ID.String(), Name: p.Name}
	}

	Ok(w, summaries)
}

// OIDCLogin handles GET /api/v1/auth/oidc/login?provider_id={id}.
// Generates the authorization URL for the given provider and redirects the
// user to the identity provider. Stores state, code verifier, and provider ID
// in short-lived httpOnly cookies for CSRF protection and PKCE.
func (h *AuthHandler) OIDCLogin(w http.ResponseWriter, r *http.Request) {
	providerIDStr := r.URL.Query().Get("provider_id")
	providerID, err := uuid.Parse(providerIDStr)
	if err != nil {
		ErrBadRequest(w, "missing or invalid provider_id")
		return
	}

	redirectURL, state, codeVerifier, err := h.svc.AuthorizationURL(r.Context(), providerID, requestCallbackURL(r))
	if err != nil {
		if errors.Is(err, auth.ErrProviderNotFound) {
			ErrBadRequest(w, "OIDC provider not found")
			return
		}
		h.logger.Error("failed to generate OIDC authorization URL", zap.Error(err))
		ErrInternal(w)
		return
	}

	expires := time.Now().Add(oidcCookieTTL)

	for _, c := range []struct{ name, value string }{
		{oidcStateCookie, state},
		{oidcVerifierCookie, codeVerifier},
		{oidcProviderCookie, providerID.String()},
	} {
		http.SetCookie(w, &http.Cookie{
			Name:     c.name,
			Value:    c.value,
			Expires:  expires,
			HttpOnly: true,
			Secure:   h.secure,
			SameSite: http.SameSiteLaxMode,
			Path:     "/",
		})
	}

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// OIDCCallback handles GET /api/v1/auth/oidc/callback.
// Completes the Authorization Code + PKCE flow using the provider ID, state,
// and verifier stored in the session cookies set by OIDCLogin.
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

	providerCookie, err := r.Cookie(oidcProviderCookie)
	if err != nil {
		ErrBadRequest(w, "missing OIDC provider cookie")
		return
	}

	h.clearOIDCCookies(w)

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" || state == "" {
		ErrBadRequest(w, "missing code or state parameter")
		return
	}

	pair, err := h.svc.ExchangeCode(r.Context(), auth.OIDCCallbackRequest{
		ProviderID:   providerCookie.Value,
		CallbackURL:  requestCallbackURL(r),
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

	expiresIn := int(time.Until(pair.AccessTokenExpiresAt).Seconds())
	redirectURL := fmt.Sprintf("/auth/callback?token=%s&expires_in=%d", pair.AccessToken, expiresIn)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// -----------------------------------------------------------------------------
// Cookie helpers
// -----------------------------------------------------------------------------

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

func (h *AuthHandler) clearOIDCCookies(w http.ResponseWriter) {
	for _, name := range []string{oidcStateCookie, oidcVerifierCookie, oidcProviderCookie} {
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
