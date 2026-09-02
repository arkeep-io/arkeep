package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// TwoFactorHandler implements the self-service two-factor authentication
// enrollment flow (setup/verify/disable/regenerate) and the admin reset. The
// login-time challenge (step two of a two-factor login) is handled by
// AuthHandler instead, since it runs before the user is authenticated.
type TwoFactorHandler struct {
	users         repositories.UserRepository
	challenges    repositories.TwoFactorChallengeRepository
	recoveryCodes repositories.RecoveryCodeRepository
	refresh       repositories.RefreshTokenRepository
	auditRepo     repositories.AuditRepository
	logger        *zap.Logger
}

// NewTwoFactorHandler creates a new TwoFactorHandler.
func NewTwoFactorHandler(
	users repositories.UserRepository,
	challenges repositories.TwoFactorChallengeRepository,
	recoveryCodes repositories.RecoveryCodeRepository,
	refresh repositories.RefreshTokenRepository,
	auditRepo repositories.AuditRepository,
	logger *zap.Logger,
) *TwoFactorHandler {
	return &TwoFactorHandler{
		users:         users,
		challenges:    challenges,
		recoveryCodes: recoveryCodes,
		refresh:       refresh,
		auditRepo:     auditRepo,
		logger:        logger.Named("two_factor_handler"),
	}
}

// currentUser loads the authenticated user from claims, or writes an error
// response and reports ok=false.
func (h *TwoFactorHandler) currentUser(w http.ResponseWriter, r *http.Request) (*db.User, bool) {
	claims := claimsFromCtx(r.Context())
	if claims == nil {
		ErrUnauthorized(w)
		return nil, false
	}
	id, err := parseUUIDString(claims.UserID)
	if err != nil {
		ErrInternal(w)
		return nil, false
	}
	user, err := h.users.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return nil, false
		}
		h.logger.Error("failed to load current user", zap.Error(err))
		ErrInternal(w)
		return nil, false
	}
	return user, true
}

// requireLocalAccount rejects the request with 400 if the user authenticates
// via OIDC — two-factor is managed by the identity provider for those
// accounts, matching the same gate password reset uses.
func requireLocalAccount(w http.ResponseWriter, user *db.User) bool {
	if user.OIDCProvider != "" || user.Password == "" {
		ErrBadRequest(w, "two-factor authentication is managed by your identity provider")
		return false
	}
	return true
}

// -----------------------------------------------------------------------------
// Status
// -----------------------------------------------------------------------------

type twoFactorStatusResponse struct {
	Enabled                bool  `json:"enabled"`
	Pending                bool  `json:"pending"`
	RecoveryCodesRemaining int64 `json:"recovery_codes_remaining"`
}

// Status handles GET /api/v1/auth/2fa/status.
func (h *TwoFactorHandler) Status(w http.ResponseWriter, r *http.Request) {
	user, ok := h.currentUser(w, r)
	if !ok {
		return
	}

	resp := twoFactorStatusResponse{
		Enabled: user.TwoFactorEnabled,
		Pending: !user.TwoFactorEnabled && user.TOTPSecret != "",
	}
	if user.TwoFactorEnabled {
		count, err := h.recoveryCodes.CountUnused(r.Context(), user.ID)
		if err != nil {
			h.logger.Error("failed to count recovery codes", zap.Error(err))
			ErrInternal(w)
			return
		}
		resp.RecoveryCodesRemaining = count
	}
	Ok(w, resp)
}

// -----------------------------------------------------------------------------
// Setup
// -----------------------------------------------------------------------------

type twoFactorSetupResponse struct {
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauth_url"`
}

// Setup handles POST /api/v1/auth/2fa/setup. It generates a new TOTP secret
// and stores it unconfirmed — TwoFactorEnabled stays false until Verify
// succeeds. Calling it again before Verify overwrites the pending secret, so a
// user who scans the wrong QR code can just start over.
func (h *TwoFactorHandler) Setup(w http.ResponseWriter, r *http.Request) {
	user, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	if !requireLocalAccount(w, user) {
		return
	}
	if user.TwoFactorEnabled {
		ErrConflict(w, "two-factor authentication is already enabled")
		return
	}

	secret, otpauthURL, err := auth.NewTOTPKey(user.Email)
	if err != nil {
		h.logger.Error("failed to generate TOTP key", zap.Error(err))
		ErrInternal(w)
		return
	}

	user.TOTPSecret = db.EncryptedString(secret)
	if err := h.users.Update(r.Context(), user); err != nil {
		h.logger.Error("failed to persist pending TOTP secret", zap.Error(err))
		ErrInternal(w)
		return
	}

	Ok(w, twoFactorSetupResponse{Secret: secret, OTPAuthURL: otpauthURL})
}

// -----------------------------------------------------------------------------
// Verify
// -----------------------------------------------------------------------------

type twoFactorVerifyRequest struct {
	Code string `json:"code"`
}

type twoFactorRecoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

// Verify handles POST /api/v1/auth/2fa/verify. It confirms the pending secret
// from Setup, activates two-factor, issues a fresh set of recovery codes, and
// revokes every other session for the account — a device that stole the
// access token before 2FA was enabled loses access, while the session that
// just enrolled stays logged in.
func (h *TwoFactorHandler) Verify(w http.ResponseWriter, r *http.Request) {
	user, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	if !requireLocalAccount(w, user) {
		return
	}

	var req twoFactorVerifyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if user.TOTPSecret == "" {
		ErrBadRequest(w, "call setup before verify")
		return
	}
	if !auth.ValidateTOTPCode(string(user.TOTPSecret), req.Code) {
		ErrBadRequest(w, "invalid code")
		return
	}

	ctx := r.Context()
	codes, err := h.replaceRecoveryCodes(ctx, user.ID)
	if err != nil {
		h.logger.Error("failed to generate recovery codes", zap.Error(err))
		ErrInternal(w)
		return
	}

	user.TwoFactorEnabled = true
	if err := h.users.Update(ctx, user); err != nil {
		h.logger.Error("failed to enable two-factor", zap.Error(err))
		ErrInternal(w)
		return
	}

	// Keep the session that just enrolled logged in; close every other one.
	keepHash := ""
	if cookie, err := r.Cookie(refreshTokenCookie); err == nil {
		keepHash = auth.HashToken(cookie.Value)
	}
	if err := h.refresh.RevokeAllForUserExcept(ctx, user.ID, keepHash); err != nil {
		h.logger.Warn("failed to revoke other sessions after enabling 2fa", zap.Error(err))
	}

	logAudit(r, h.auditRepo, h.logger, "auth.2fa.enabled", "user", user.ID.String(), map[string]any{})
	Ok(w, twoFactorRecoveryCodesResponse{RecoveryCodes: codes})
}

// -----------------------------------------------------------------------------
// Disable
// -----------------------------------------------------------------------------

type twoFactorDisableRequest struct {
	Password string `json:"password"`
}

// Disable handles POST /api/v1/auth/2fa/disable. It requires the current
// password so a hijacked, still-valid access token cannot turn off two-factor
// on its own.
func (h *TwoFactorHandler) Disable(w http.ResponseWriter, r *http.Request) {
	user, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	if !requireLocalAccount(w, user) {
		return
	}

	var req twoFactorDisableRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !auth.VerifyPassword(req.Password, string(user.Password)) {
		ErrBadRequest(w, "incorrect password")
		return
	}

	if err := h.clearTwoFactor(r.Context(), user); err != nil {
		h.logger.Error("failed to disable two-factor", zap.Error(err))
		ErrInternal(w)
		return
	}

	logAudit(r, h.auditRepo, h.logger, "auth.2fa.disabled", "user", user.ID.String(), map[string]any{})
	NoContent(w)
}

// clearTwoFactor resets a user's two-factor state: the secret, the enabled
// flag, every recovery code, and any outstanding login challenge. Shared by
// Disable and AdminReset.
func (h *TwoFactorHandler) clearTwoFactor(ctx context.Context, user *db.User) error {
	user.TOTPSecret = ""
	user.TwoFactorEnabled = false
	if err := h.users.Update(ctx, user); err != nil {
		return err
	}
	if err := h.recoveryCodes.DeleteByUserID(ctx, user.ID); err != nil {
		return err
	}
	return h.challenges.DeleteByUserID(ctx, user.ID)
}

// -----------------------------------------------------------------------------
// Regenerate recovery codes
// -----------------------------------------------------------------------------

type twoFactorRegenerateRequest struct {
	Password string `json:"password"`
}

// RegenerateRecoveryCodes handles
// POST /api/v1/auth/2fa/recovery-codes/regenerate. It replaces the whole set
// of recovery codes, invalidating every old one, and requires the current
// password for the same reason Disable does.
func (h *TwoFactorHandler) RegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	user, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	if !user.TwoFactorEnabled {
		ErrBadRequest(w, "two-factor authentication is not enabled")
		return
	}

	var req twoFactorRegenerateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !auth.VerifyPassword(req.Password, string(user.Password)) {
		ErrBadRequest(w, "incorrect password")
		return
	}

	codes, err := h.replaceRecoveryCodes(r.Context(), user.ID)
	if err != nil {
		h.logger.Error("failed to generate recovery codes", zap.Error(err))
		ErrInternal(w)
		return
	}

	logAudit(r, h.auditRepo, h.logger, "auth.2fa.recovery_regenerated", "user", user.ID.String(), map[string]any{})
	Ok(w, twoFactorRecoveryCodesResponse{RecoveryCodes: codes})
}

// replaceRecoveryCodes deletes every existing recovery code for userID and
// persists a freshly generated set, returning the raw codes to show the user
// once. Shared by Verify and RegenerateRecoveryCodes.
func (h *TwoFactorHandler) replaceRecoveryCodes(ctx context.Context, userID uuid.UUID) ([]string, error) {
	codes, err := auth.GenerateRecoveryCodes(auth.RecoveryCodeCount)
	if err != nil {
		return nil, err
	}

	batch := make([]db.RecoveryCode, len(codes))
	for i, code := range codes {
		batch[i] = db.RecoveryCode{
			UserID:   userID,
			CodeHash: auth.HashToken(auth.NormaliseRecoveryCode(code)),
		}
	}

	if err := h.recoveryCodes.DeleteByUserID(ctx, userID); err != nil {
		return nil, err
	}
	if err := h.recoveryCodes.CreateBatch(ctx, batch); err != nil {
		return nil, err
	}
	return codes, nil
}

// -----------------------------------------------------------------------------
// Admin reset
// -----------------------------------------------------------------------------

// AdminReset handles POST /api/v1/users/{id}/2fa/reset (admin only). It turns
// off two-factor on another user's account — e.g. when they lose both their
// authenticator and their recovery codes — and revokes all of that user's
// sessions, since whoever can now log in with just a password should not
// inherit an already-authenticated session.
func (h *TwoFactorHandler) AdminReset(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	user, err := h.users.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to load user for 2fa reset", zap.Error(err))
		ErrInternal(w)
		return
	}

	if err := h.clearTwoFactor(r.Context(), user); err != nil {
		h.logger.Error("failed to reset two-factor", zap.Error(err))
		ErrInternal(w)
		return
	}
	if err := h.refresh.RevokeAllForUser(r.Context(), user.ID); err != nil {
		h.logger.Warn("failed to revoke sessions after admin 2fa reset", zap.Error(err))
	}

	logAudit(r, h.auditRepo, h.logger, "user.2fa.reset", "user", user.ID.String(), map[string]any{"email": user.Email})
	NoContent(w)
}
