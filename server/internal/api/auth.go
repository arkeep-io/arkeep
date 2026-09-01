package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
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

	// twoFactorChallengeTTL is how long the interim login challenge is valid.
	twoFactorChallengeTTL = 5 * time.Minute

	// maxTwoFactorAttempts is how many wrong codes a single challenge tolerates
	// before it is burned. This is the per-account lockout: the per-IP rate
	// limiter alone is weak against a six-digit code.
	maxTwoFactorAttempts = 5

	// invalidTwoFactorMessage is deliberately identical for an unknown,
	// expired, consumed or burned challenge and for a wrong code.
	invalidTwoFactorMessage = "invalid or expired code"
)

// totpCodePattern distinguishes a six-digit TOTP value from a recovery code.
var totpCodePattern = regexp.MustCompile(`^[0-9]{6}$`)

// AuthHandler groups all authentication-related HTTP handlers.
type AuthHandler struct {
	svc           *auth.AuthService
	users         repositories.UserRepository
	challenges    repositories.TwoFactorChallengeRepository
	recoveryCodes repositories.RecoveryCodeRepository
	auditRepo     repositories.AuditRepository
	logger        *zap.Logger
	secure        bool // true in production (HTTPS), false in development
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(
	svc *auth.AuthService,
	users repositories.UserRepository,
	challenges repositories.TwoFactorChallengeRepository,
	recoveryCodes repositories.RecoveryCodeRepository,
	auditRepo repositories.AuditRepository,
	logger *zap.Logger,
	secure bool,
) *AuthHandler {
	return &AuthHandler{
		svc:           svc,
		users:         users,
		challenges:    challenges,
		recoveryCodes: recoveryCodes,
		auditRepo:     auditRepo,
		logger:        logger.Named("auth_handler"),
		secure:        secure,
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
	AccessToken string `json:"access_token,omitempty"`
	ExpiresIn   int    `json:"expires_in,omitempty"`

	// TwoFactorRequired and ChallengeToken are set instead of AccessToken when
	// the password was correct but the account needs a second factor. The
	// client posts ChallengeToken back to /auth/login/2fa with the code.
	TwoFactorRequired bool   `json:"two_factor_required,omitempty"`
	ChallengeToken    string `json:"challenge_token,omitempty"`
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
		var twoFactor *auth.TwoFactorRequiredError
		if errors.As(err, &twoFactor) {
			h.startTwoFactorChallenge(w, r, twoFactor.UserID)
			return
		}
		if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrUserDisabled) {
			ErrUnauthorized(w)
			return
		}
		h.logger.Error("login failed", zap.String("email", req.Email), zap.Error(err))
		ErrInternal(w)
		return
	}

	// Log the login event. The JWT claims must be parsed from the issued token
	// because the Authenticate middleware hasn't run yet at this point.
	if claims, err := h.svc.ValidateAccessToken(pair.AccessToken); err == nil {
		if uid, parseErr := uuid.Parse(claims.UserID); parseErr == nil {
			logAuditDirect(r, h.auditRepo, h.logger, uid, claims.Email, "auth.login", "user", claims.UserID, map[string]any{"email": req.Email})
		}
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
	logAudit(r, h.auditRepo, h.logger, "auth.logout", "user", "", map[string]any{})
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
// Two-factor login (step two)
// -----------------------------------------------------------------------------

// startTwoFactorChallenge issues the interim challenge after a correct password
// on a two-factor account. Any outstanding challenge for the user is deleted
// first, so only the newest login attempt can be completed.
func (h *AuthHandler) startTwoFactorChallenge(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	ctx := r.Context()

	// GenerateResetToken is the package's generic 32-byte opaque token helper;
	// the name reflects its first caller, not a restriction.
	raw, err := auth.GenerateResetToken()
	if err != nil {
		h.logger.Error("2fa login: challenge token generation failed", zap.Error(err))
		ErrInternal(w)
		return
	}

	if err := h.challenges.DeleteByUserID(ctx, userID); err != nil {
		h.logger.Error("2fa login: clearing old challenges failed", zap.Error(err))
		ErrInternal(w)
		return
	}

	if err := h.challenges.Create(ctx, &db.TwoFactorChallenge{
		UserID:    userID,
		TokenHash: auth.HashToken(raw),
		ExpiresAt: time.Now().Add(twoFactorChallengeTTL),
	}); err != nil {
		h.logger.Error("2fa login: persisting challenge failed", zap.Error(err))
		ErrInternal(w)
		return
	}

	Ok(w, loginResponse{TwoFactorRequired: true, ChallengeToken: raw})
}

type loginTwoFactorRequest struct {
	ChallengeToken string `json:"challenge_token"`
	Code           string `json:"code"`
}

// LoginTwoFactor handles POST /api/v1/auth/login/2fa.
// It completes a login started by Login, accepting either a TOTP code or a
// recovery code. Failures return 400 rather than 401: the GUI's api() wrapper
// treats 401 as an expired session and silently retries after a token refresh,
// which would swallow the error.
func (h *AuthHandler) LoginTwoFactor(w http.ResponseWriter, r *http.Request) {
	var req loginTwoFactorRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ChallengeToken == "" || req.Code == "" {
		ErrBadRequest(w, "challenge_token and code are required")
		return
	}

	ctx := r.Context()

	challenge, err := h.challenges.GetUnusedByHash(ctx, auth.HashToken(req.ChallengeToken))
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrBadRequest(w, invalidTwoFactorMessage)
			return
		}
		h.logger.Error("2fa login: challenge lookup failed", zap.Error(err))
		ErrInternal(w)
		return
	}

	if time.Now().After(challenge.ExpiresAt) {
		ErrBadRequest(w, invalidTwoFactorMessage)
		return
	}

	user, err := h.users.GetByID(ctx, challenge.UserID)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrBadRequest(w, invalidTwoFactorMessage)
			return
		}
		h.logger.Error("2fa login: user lookup failed", zap.Error(err))
		ErrInternal(w)
		return
	}

	if !user.IsActive {
		ErrUnauthorized(w)
		return
	}

	valid, usedRecovery := h.verifySecondFactor(ctx, user, req.Code)
	if !valid {
		h.registerFailedAttempt(ctx, challenge)
		logAuditDirect(r, h.auditRepo, h.logger, user.ID, user.Email,
			"auth.2fa.challenge_failed", "user", user.ID.String(), map[string]any{})
		ErrBadRequest(w, invalidTwoFactorMessage)
		return
	}

	// Consume the challenge before issuing tokens so it cannot be replayed.
	if err := h.challenges.MarkUsed(ctx, challenge.ID); err != nil {
		h.logger.Warn("2fa login: marking challenge used failed", zap.Error(err))
	}

	pair, err := h.svc.IssueTokenPairForUser(ctx, user)
	if err != nil {
		h.logger.Error("2fa login: issuing tokens failed", zap.Error(err))
		ErrInternal(w)
		return
	}

	method := "totp"
	if usedRecovery {
		method = "recovery_code"
		logAuditDirect(r, h.auditRepo, h.logger, user.ID, user.Email,
			"auth.2fa.recovery_used", "user", user.ID.String(), map[string]any{})
	}
	logAuditDirect(r, h.auditRepo, h.logger, user.ID, user.Email,
		"auth.login", "user", user.ID.String(), map[string]any{"email": user.Email, "method": method})

	h.setRefreshCookie(w, pair.RefreshToken, pair.RefreshTokenExpiresAt)
	Ok(w, loginResponse{
		AccessToken: pair.AccessToken,
		ExpiresIn:   int(time.Until(pair.AccessTokenExpiresAt).Seconds()),
	})
}

// verifySecondFactor accepts a six-digit TOTP value or a recovery code. The
// second return value reports whether a recovery code was consumed.
func (h *AuthHandler) verifySecondFactor(ctx context.Context, user *db.User, code string) (valid, usedRecovery bool) {
	if totpCodePattern.MatchString(code) {
		return auth.ValidateTOTPCode(string(user.TOTPSecret), code), false
	}

	stored, err := h.recoveryCodes.GetUnusedByHash(ctx, auth.HashToken(auth.NormaliseRecoveryCode(code)))
	if err != nil {
		return false, false
	}
	// A code belonging to another account must never satisfy this challenge.
	if stored.UserID != user.ID {
		return false, false
	}
	if err := h.recoveryCodes.MarkUsed(ctx, stored.ID); err != nil {
		h.logger.Error("2fa login: marking recovery code used failed", zap.Error(err))
		return false, false
	}
	return true, true
}

// registerFailedAttempt counts a wrong code against the challenge and burns it
// once maxTwoFactorAttempts is reached, forcing the password step to be redone.
func (h *AuthHandler) registerFailedAttempt(ctx context.Context, challenge *db.TwoFactorChallenge) {
	if challenge.Attempts+1 >= maxTwoFactorAttempts {
		if err := h.challenges.MarkUsed(ctx, challenge.ID); err != nil {
			h.logger.Warn("2fa login: burning challenge failed", zap.Error(err))
		}
		return
	}
	if err := h.challenges.IncrementAttempts(ctx, challenge.ID); err != nil {
		h.logger.Warn("2fa login: incrementing attempts failed", zap.Error(err))
	}
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
