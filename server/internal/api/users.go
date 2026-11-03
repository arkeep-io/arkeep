package api

import (
	"errors"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// UserHandler groups all user-related HTTP handlers.
// Admin-only routes (List, Create, GetByID, Update, Delete) are protected by
// RequireRole("admin") in the router. The /users/me routes are accessible by
// any authenticated user.
type UserHandler struct {
	repo            repositories.UserRepository
	refresh         repositories.RefreshTokenRepository
	passwordChanges repositories.PasswordChangeChallengeRepository
	auditRepo       repositories.AuditRepository
	logger          *zap.Logger
}

// NewUserHandler creates a new UserHandler.
func NewUserHandler(repo repositories.UserRepository, refresh repositories.RefreshTokenRepository, passwordChanges repositories.PasswordChangeChallengeRepository, auditRepo repositories.AuditRepository, logger *zap.Logger) *UserHandler {
	return &UserHandler{
		repo:            repo,
		refresh:         refresh,
		passwordChanges: passwordChanges,
		auditRepo:       auditRepo,
		logger:          logger.Named("user_handler"),
	}
}

// -----------------------------------------------------------------------------
// Response types
// -----------------------------------------------------------------------------

// userResponse is the JSON representation of a user.
// Password and OIDCSub are intentionally omitted — they are write-only or
// internal fields that must never be exposed via the API.
type userResponse struct {
	ID               string  `json:"id"`
	Email            string  `json:"email"`
	DisplayName      string  `json:"display_name"`
	Role             string  `json:"role"`
	IsActive         bool    `json:"is_active"`
	IsOIDC           bool    `json:"is_oidc"`
	TwoFactorEnabled bool    `json:"two_factor_enabled"`
	LastLoginAt      *string `json:"last_login_at"`
	CreatedAt        string  `json:"created_at"`

	// MustChangePassword: the user has to set a new password at next sign-in.
	MustChangePassword bool `json:"must_change_password"`
}

// userToResponse converts a db.User to a userResponse.
func userToResponse(u *db.User) userResponse {
	resp := userResponse{
		ID:               u.ID.String(),
		Email:            u.Email,
		DisplayName:      u.DisplayName,
		Role:             u.Role,
		IsActive:         u.IsActive,
		IsOIDC:           u.OIDCProvider != "",
		TwoFactorEnabled: u.TwoFactorEnabled,
		CreatedAt:        u.CreatedAt.UTC().Format(time.RFC3339),

		MustChangePassword: u.MustChangePassword,
	}
	if u.LastLoginAt != nil {
		s := u.LastLoginAt.UTC().Format(time.RFC3339)
		resp.LastLoginAt = &s
	}
	return resp
}

// listUsersResponse wraps a paginated list of users.
type listUsersResponse struct {
	Items []userResponse `json:"items"`
	Total int64          `json:"total"`
}

// -----------------------------------------------------------------------------
// Admin handlers
// -----------------------------------------------------------------------------

// List handles GET /api/v1/users (admin only).
func (h *UserHandler) List(w http.ResponseWriter, r *http.Request) {
	opts := paginationOpts(r)

	users, total, err := h.repo.List(r.Context(), opts)
	if err != nil {
		h.logger.Error("failed to list users", zap.Error(err))
		ErrInternal(w)
		return
	}

	items := make([]userResponse, len(users))
	for i := range users {
		items[i] = userToResponse(&users[i])
	}

	Ok(w, listUsersResponse{Items: items, Total: total})
}

// createUserRequest is the JSON body expected by POST /api/v1/users.
type createUserRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"` // "admin" or "user"
}

// Create handles POST /api/v1/users (admin only).
// Creates a local user account with an Argon2id-hashed password.
// OIDC users are provisioned automatically via the OIDC callback — this
// endpoint is for local accounts only.
func (h *UserHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Email == "" {
		ErrBadRequest(w, "email is required")
		return
	}
	if req.Password == "" {
		ErrBadRequest(w, "password is required")
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		ErrBadRequest(w, err.Error())
		return
	}
	if req.DisplayName == "" {
		ErrBadRequest(w, "display_name is required")
		return
	}
	if req.Role != "admin" && req.Role != "user" {
		ErrBadRequest(w, "role must be 'admin' or 'user'")
		return
	}

	hashed, err := auth.HashPassword(req.Password)
	if err != nil {
		h.logger.Error("failed to hash password", zap.Error(err))
		ErrInternal(w)
		return
	}

	user := &db.User{
		Email:       req.Email,
		Password:    db.EncryptedString(hashed),
		DisplayName: req.DisplayName,
		Role:        req.Role,
		IsActive:    true,
	}

	if err := h.repo.Create(r.Context(), user); err != nil {
		if errors.Is(err, repositories.ErrConflict) {
			ErrConflict(w, "a user with this email already exists")
			return
		}
		h.logger.Error("failed to create user", zap.Error(err))
		ErrInternal(w)
		return
	}

	logAudit(r, h.auditRepo, h.logger, "user.create", "user", user.ID.String(), map[string]any{"email": user.Email, "role": user.Role})
	Created(w, userToResponse(user))
}

// GetByID handles GET /api/v1/users/{id} (admin only).
func (h *UserHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	user, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get user", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	Ok(w, userToResponse(user))
}

// updateUserRequest is the JSON body for PATCH /api/v1/users/{id} (admin only).
// All fields are optional. The password is not one of them: an admin sets it
// with SetPassword, which revokes the user's sessions and is audited.
type updateUserRequest struct {
	DisplayName *string `json:"display_name"`
	Role        *string `json:"role"`
	IsActive    *bool   `json:"is_active"`
}

// Update handles PATCH /api/v1/users/{id} (admin only).
func (h *UserHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	var req updateUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	user, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get user for update", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	wasActiveAdmin := user.Role == "admin" && user.IsActive

	if req.DisplayName != nil {
		if *req.DisplayName == "" {
			ErrBadRequest(w, "display_name cannot be empty")
			return
		}
		user.DisplayName = *req.DisplayName
	}
	if req.Role != nil {
		if *req.Role != "admin" && *req.Role != "user" {
			ErrBadRequest(w, "role must be 'admin' or 'user'")
			return
		}
		user.Role = *req.Role
	}
	if req.IsActive != nil {
		user.IsActive = *req.IsActive
	}

	// The last active admin can be neither demoted nor deactivated: nobody
	// could administer Arkeep afterwards. An OIDC login refuses the same
	// (OIDCAuthProvider.canChangeRole).
	if wasActiveAdmin && (user.Role != "admin" || !user.IsActive) {
		n, err := h.repo.CountActiveAdmins(r.Context())
		if err != nil {
			h.logger.Error("failed to count active admins", zap.Error(err))
			ErrInternal(w)
			return
		}
		if n <= 1 {
			ErrConflict(w, "the last active admin cannot be demoted or deactivated")
			return
		}
	}

	if err := h.repo.Update(r.Context(), user); err != nil {
		h.logger.Error("failed to update user", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	logAudit(r, h.auditRepo, h.logger, "user.update", "user", id.String(), map[string]any{"email": user.Email, "role": user.Role})
	Ok(w, userToResponse(user))
}

// setPasswordRequest is the JSON body for POST /api/v1/users/{id}/password.
type setPasswordRequest struct {
	Password string `json:"password"`
	// MustChange makes the user set a new password at next sign-in.
	MustChange bool `json:"must_change"`
}

// SetPassword handles POST /api/v1/users/{id}/password (admin only). It sets
// another local user's password — e.g. when they forgot it and no SMTP server
// is configured for the reset link — and revokes all of that user's sessions,
// like AdminReset does for two-factor. Refused on the admin's own account (the
// Profile page asks for the current password) and on OIDC accounts (the
// identity provider owns their password).
func (h *UserHandler) SetPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	claims := claimsFromCtx(r.Context())
	if claims != nil && claims.UserID == id.String() {
		ErrBadRequest(w, "change your own password from your profile")
		return
	}

	var req setPasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		ErrBadRequest(w, err.Error())
		return
	}

	ctx := r.Context()
	user, err := h.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to load user for set password", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}
	if user.OIDCProvider != "" {
		ErrBadRequest(w, "password cannot be set for OIDC accounts")
		return
	}

	hashed, err := auth.HashPassword(req.Password)
	if err != nil {
		h.logger.Error("failed to hash password", zap.Error(err))
		ErrInternal(w)
		return
	}
	user.Password = db.EncryptedString(hashed)
	user.MustChangePassword = req.MustChange
	if err := h.repo.Update(ctx, user); err != nil {
		h.logger.Error("failed to set user password", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	// Whoever signed in with the old password keeps no session, and a password
	// change step started before this one cannot complete.
	if err := h.refresh.RevokeAllForUser(ctx, user.ID); err != nil {
		h.logger.Warn("failed to revoke sessions after set password", zap.Error(err))
	}
	if err := h.passwordChanges.DeleteByUserID(ctx, user.ID); err != nil {
		h.logger.Warn("failed to clear password change challenges after set password", zap.Error(err))
	}

	logAudit(r, h.auditRepo, h.logger, "user.password_set", "user", user.ID.String(),
		map[string]any{"email": user.Email, "must_change": req.MustChange})
	Ok(w, userToResponse(user))
}

// Delete handles DELETE /api/v1/users/{id} (admin only).
// Permanently removes the user record.
func (h *UserHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	// Prevent admins from deleting their own account to avoid lockout.
	claims := claimsFromCtx(r.Context())
	if claims != nil && claims.UserID == id.String() {
		ErrBadRequest(w, "cannot delete your own account")
		return
	}

	// Fetch the user before deletion so we can include the email in the audit record.
	targetUser, _ := h.repo.GetByID(r.Context(), id)

	if err := h.repo.Delete(r.Context(), id); err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to delete user", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	email := ""
	if targetUser != nil {
		email = targetUser.Email
	}
	logAudit(r, h.auditRepo, h.logger, "user.delete", "user", id.String(), map[string]any{"email": email})
	NoContent(w)
}

// -----------------------------------------------------------------------------
// Current user handlers
// -----------------------------------------------------------------------------

// GetMe handles GET /api/v1/users/me.
// Returns the profile of the currently authenticated user.
func (h *UserHandler) GetMe(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	if claims == nil {
		ErrUnauthorized(w)
		return
	}

	id, err := parseUUIDString(claims.UserID)
	if err != nil {
		ErrInternal(w)
		return
	}

	user, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get current user", zap.String("id", claims.UserID), zap.Error(err))
		ErrInternal(w)
		return
	}

	Ok(w, userToResponse(user))
}

// updateMeRequest is the JSON body for PATCH /api/v1/users/me.
// Users can only update their own display name and password — not role or
// active status.
type updateMeRequest struct {
	DisplayName *string `json:"display_name"`
	Password    *string `json:"password"`
	// CurrentPassword is required with Password: a stolen access token alone
	// must not be enough to take the account over (SEC-10).
	CurrentPassword string `json:"current_password"`
}

// UpdateMe handles PATCH /api/v1/users/me.
// Allows the current user to update their own display name and password.
func (h *UserHandler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	if claims == nil {
		ErrUnauthorized(w)
		return
	}

	var req updateMeRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	id, err := parseUUIDString(claims.UserID)
	if err != nil {
		ErrInternal(w)
		return
	}

	user, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get user for self-update", zap.String("id", claims.UserID), zap.Error(err))
		ErrInternal(w)
		return
	}

	// OIDC users cannot change their password — it is managed by the IdP.
	if req.Password != nil && user.OIDCProvider != "" {
		ErrBadRequest(w, "password cannot be changed for OIDC accounts")
		return
	}
	// 400 rather than 401 for a wrong current password: the GUI treats 401
	// as an expired session and would retry after a refresh.
	if req.Password != nil && !auth.VerifyPassword(req.CurrentPassword, string(user.Password)) {
		logAudit(r, h.auditRepo, h.logger, "user.password_change_failed", "user", user.ID.String(), map[string]any{})
		ErrBadRequest(w, "current password is incorrect")
		return
	}

	if req.DisplayName != nil {
		if *req.DisplayName == "" {
			ErrBadRequest(w, "display_name cannot be empty")
			return
		}
		user.DisplayName = *req.DisplayName
	}
	if req.Password != nil {
		if err := auth.ValidatePassword(*req.Password); err != nil {
			ErrBadRequest(w, err.Error())
			return
		}
		hashed, err := auth.HashPassword(*req.Password)
		if err != nil {
			h.logger.Error("failed to hash password", zap.Error(err))
			ErrInternal(w)
			return
		}
		user.Password = db.EncryptedString(hashed)
		user.MustChangePassword = false
	}

	if err := h.repo.Update(r.Context(), user); err != nil {
		h.logger.Error("failed to update current user", zap.String("id", claims.UserID), zap.Error(err))
		ErrInternal(w)
		return
	}

	if req.Password != nil {
		// Keep the session that changed the password; close every other one.
		keepHash := ""
		if cookie, err := r.Cookie(refreshTokenCookie); err == nil {
			keepHash = auth.HashToken(cookie.Value)
		}
		if err := h.refresh.RevokeAllForUserExcept(r.Context(), user.ID, keepHash); err != nil {
			h.logger.Warn("failed to revoke other sessions after a password change", zap.Error(err))
		}
		logAudit(r, h.auditRepo, h.logger, "user.password_changed", "user", user.ID.String(), map[string]any{})
	}

	Ok(w, userToResponse(user))
}
