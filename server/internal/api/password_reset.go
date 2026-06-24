package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// resetTokenTTL is how long a password reset link remains valid.
const resetTokenTTL = time.Hour

// resetMinPasswordLen is the minimum length enforced for a new password set via
// the reset flow. Matches the frontend validation on the setup/reset forms.
const resetMinPasswordLen = 8

// genericResetMessage is returned for every password reset request, regardless
// of whether the email maps to a local account. This prevents attackers from
// using the endpoint to enumerate registered email addresses.
const genericResetMessage = "if an account with that email exists, a password reset link has been sent"

// Mailer is the minimal email capability the password reset flow depends on.
// It is satisfied by *notification.NotificationService.
type Mailer interface {
	// SMTPConfigured reports whether a usable SMTP configuration exists.
	SMTPConfigured(ctx context.Context) bool
	// SendEmail delivers a one-off email to the given recipients.
	SendEmail(ctx context.Context, to []string, subject, body string) error
}

// PasswordResetHandler implements the self-service "forgot password" flow for
// local accounts. OIDC users are managed by their identity provider and are
// silently ignored (still receiving the generic response).
type PasswordResetHandler struct {
	users     repositories.UserRepository
	tokens    repositories.PasswordResetTokenRepository
	refresh   repositories.RefreshTokenRepository
	mailer    Mailer
	auditRepo repositories.AuditRepository
	logger    *zap.Logger
	// baseURL is the trusted external base URL (scheme://host) used to build the
	// reset link embedded in outbound email. When empty it falls back to the
	// request-derived host — see resetLinkBase for the security rationale.
	baseURL string
}

// NewPasswordResetHandler creates a PasswordResetHandler. baseURL is the
// server-configured external URL (e.g. https://arkeep.example.com); pass an
// empty string to derive it from the request.
func NewPasswordResetHandler(
	users repositories.UserRepository,
	tokens repositories.PasswordResetTokenRepository,
	refresh repositories.RefreshTokenRepository,
	mailer Mailer,
	auditRepo repositories.AuditRepository,
	logger *zap.Logger,
	baseURL string,
) *PasswordResetHandler {
	return &PasswordResetHandler{
		users:     users,
		tokens:    tokens,
		refresh:   refresh,
		mailer:    mailer,
		auditRepo: auditRepo,
		logger:    logger.Named("password_reset"),
		baseURL:   strings.TrimRight(baseURL, "/"),
	}
}

// resetLinkBase returns the base URL used to build the reset link. A
// server-configured baseURL always wins: deriving the host from request headers
// (Host / X-Forwarded-Host) for an emailed link enables "password reset
// poisoning", where an attacker triggers a reset for a victim with a forged
// Host header so the victim receives a genuine email pointing at the attacker's
// domain. Only when no base URL is configured do we fall back to the request —
// matching the OIDC callback behaviour and the documented reverse-proxy
// assumption (the proxy must strip client-supplied X-Forwarded-* headers).
func (h *PasswordResetHandler) resetLinkBase(r *http.Request) string {
	if h.baseURL != "" {
		return h.baseURL
	}
	return requestBaseURL(r)
}

type passwordResetStatusResponse struct {
	// SMTPConfigured tells the frontend whether the email-based reset can work.
	// When false, the forgot-password page shows a "contact your administrator"
	// message instead of the request form.
	SMTPConfigured bool `json:"smtp_configured"`
}

// Status handles GET /api/v1/auth/password-reset/status (public).
func (h *PasswordResetHandler) Status(w http.ResponseWriter, r *http.Request) {
	Ok(w, passwordResetStatusResponse{SMTPConfigured: h.mailer.SMTPConfigured(r.Context())})
}

type passwordResetRequest struct {
	Email string `json:"email"`
}

// Request handles POST /api/v1/auth/password-reset/request (public).
// It always returns the same generic 200 response so the endpoint cannot be
// used to discover which email addresses have local accounts.
func (h *PasswordResetHandler) Request(w http.ResponseWriter, r *http.Request) {
	var req passwordResetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Email == "" {
		ErrBadRequest(w, "email is required")
		return
	}

	// All branches below converge on the same generic response. Any failure to
	// actually send is logged server-side but never surfaced to the caller.
	h.processRequest(r, req.Email)

	Ok(w, map[string]string{"message": genericResetMessage})
}

// processRequest performs the side effects of a reset request. It returns
// nothing: callers always reply with the generic message.
func (h *PasswordResetHandler) processRequest(r *http.Request, email string) {
	ctx := r.Context()

	user, err := h.users.GetByEmail(ctx, email)
	if err != nil {
		if !errors.Is(err, repositories.ErrNotFound) {
			h.logger.Error("password reset: lookup failed", zap.Error(err))
		}
		return // unknown email — generic response, no signal
	}

	// Reset only applies to active local accounts. OIDC users (empty password
	// and/or linked provider) authenticate through their identity provider.
	if !user.IsActive || user.Password == "" || user.OIDCProvider != "" {
		return
	}

	raw, err := auth.GenerateResetToken()
	if err != nil {
		h.logger.Error("password reset: token generation failed", zap.Error(err))
		return
	}

	// Invalidate any previously issued links before storing the new one.
	if err := h.tokens.DeleteByUserID(ctx, user.ID); err != nil {
		h.logger.Error("password reset: clearing old tokens failed", zap.Error(err))
		return
	}

	if err := h.tokens.Create(ctx, &db.PasswordResetToken{
		UserID:    user.ID,
		TokenHash: auth.HashToken(raw),
		ExpiresAt: time.Now().Add(resetTokenTTL),
	}); err != nil {
		h.logger.Error("password reset: persisting token failed", zap.Error(err))
		return
	}

	link := fmt.Sprintf("%s/auth/reset-password?token=%s", h.resetLinkBase(r), raw)
	subject := "Reset your Arkeep password"
	body := fmt.Sprintf(
		"A password reset was requested for your Arkeep account.\r\n\r\n"+
			"Open the link below to choose a new password. It expires in %d hour(s):\r\n\r\n"+
			"%s\r\n\r\n"+
			"If you did not request this, you can safely ignore this email — your password will not change.\r\n",
		int(resetTokenTTL.Hours()), link,
	)

	if err := h.mailer.SendEmail(ctx, []string{user.Email}, subject, body); err != nil {
		h.logger.Error("password reset: sending email failed", zap.Error(err))
		return
	}

	logAuditDirect(r, h.auditRepo, h.logger, user.ID, user.Email,
		"auth.password_reset.request", "user", user.ID.String(), map[string]any{})
}

type passwordResetConfirm struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// Confirm handles POST /api/v1/auth/password-reset/confirm (public).
// It consumes a valid token and sets the new password, then invalidates all of
// the user's existing sessions.
func (h *PasswordResetHandler) Confirm(w http.ResponseWriter, r *http.Request) {
	var req passwordResetConfirm
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Token == "" {
		ErrBadRequest(w, "token is required")
		return
	}
	if len(req.Password) < resetMinPasswordLen {
		ErrBadRequest(w, fmt.Sprintf("password must be at least %d characters", resetMinPasswordLen))
		return
	}

	ctx := r.Context()

	token, err := h.tokens.GetUnusedByHash(ctx, auth.HashToken(req.Token))
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrBadRequest(w, "invalid or expired token")
			return
		}
		h.logger.Error("password reset: token lookup failed", zap.Error(err))
		ErrInternal(w)
		return
	}

	if time.Now().After(token.ExpiresAt) {
		ErrBadRequest(w, "invalid or expired token")
		return
	}

	user, err := h.users.GetByID(ctx, token.UserID)
	if err != nil {
		// Token referenced a user that no longer exists — treat as invalid.
		if errors.Is(err, repositories.ErrNotFound) {
			ErrBadRequest(w, "invalid or expired token")
			return
		}
		h.logger.Error("password reset: user lookup failed", zap.Error(err))
		ErrInternal(w)
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		h.logger.Error("password reset: hashing password failed", zap.Error(err))
		ErrInternal(w)
		return
	}

	user.Password = db.EncryptedString(hash)
	if err := h.users.Update(ctx, user); err != nil {
		h.logger.Error("password reset: updating password failed", zap.Error(err))
		ErrInternal(w)
		return
	}

	// Consume the token so the link cannot be reused.
	if err := h.tokens.MarkUsed(ctx, token.ID); err != nil {
		h.logger.Warn("password reset: marking token used failed", zap.Error(err))
	}

	// Invalidate existing sessions — a password change should log out other
	// devices. Non-fatal: the password is already updated.
	if err := h.refresh.RevokeAllForUser(ctx, user.ID); err != nil {
		h.logger.Warn("password reset: revoking sessions failed", zap.Error(err))
	}

	logAuditDirect(r, h.auditRepo, h.logger, user.ID, user.Email,
		"auth.password_reset.confirm", "user", user.ID.String(), map[string]any{})

	Ok(w, map[string]string{"message": "password updated"})
}
