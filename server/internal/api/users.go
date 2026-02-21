package api

import (
	"errors"
	"net/http"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repository"
)

// UserHandler groups all user-related HTTP handlers.
// Admin-only routes (List, Create, GetByID, Update, Delete) are protected by
// RequireRole("admin") in the router. The /users/me routes are accessible by
// any authenticated user.
type UserHandler struct {
	repo   repository.UserRepository
	logger *zap.Logger
}

// NewUserHandler creates a new UserHandler.
func NewUserHandler(repo repository.UserRepository, logger *zap.Logger) *UserHandler {
	return &UserHandler{
		repo:   repo,
		logger: logger.Named("user_handler"),
	}
}

// -----------------------------------------------------------------------------
// Response types
// -----------------------------------------------------------------------------

// userResponse is the JSON representation of a user.
// Password and OIDCSub are intentionally omitted — they are write-only or
// internal fields that must never be exposed via the API.
type userResponse struct {
	ID          string  `json:"id"`
	Email       string  `json:"email"`
	DisplayName string  `json:"display_name"`
	Role        string  `json:"role"`
	IsActive    bool    `json:"is_active"`
	IsOIDC      bool    `json:"is_oidc"`
	LastLoginAt *string `json:"last_login_at"`
	CreatedAt   string  `json:"created_at"`
}

// userToResponse converts a db.User to a userResponse.
func userToResponse(u *db.User) userResponse {
	resp := userResponse{
		ID:          u.ID.String(),
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Role:        u.Role,
		IsActive:    u.IsActive,
		IsOIDC:      u.OIDCProvider != "",
		CreatedAt:   u.CreatedAt.UTC().String(),
	}
	if u.LastLoginAt != nil {
		s := u.LastLoginAt.UTC().String()
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
		if errors.Is(err, repository.ErrConflict) {
			ErrConflict(w, "a user with this email already exists")
			return
		}
		h.logger.Error("failed to create user", zap.Error(err))
		ErrInternal(w)
		return
	}

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
		if errors.Is(err, repository.ErrNotFound) {
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
// All fields are optional. Password triggers a rehash if provided.
type updateUserRequest struct {
	DisplayName *string `json:"display_name"`
	Role        *string `json:"role"`
	IsActive    *bool   `json:"is_active"`
	Password    *string `json:"password"`
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
		if errors.Is(err, repository.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get user for update", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

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
	if req.Password != nil {
		if *req.Password == "" {
			ErrBadRequest(w, "password cannot be empty")
			return
		}
		hashed, err := auth.HashPassword(*req.Password)
		if err != nil {
			h.logger.Error("failed to hash password", zap.Error(err))
			ErrInternal(w)
			return
		}
		user.Password = db.EncryptedString(hashed)
	}

	if err := h.repo.Update(r.Context(), user); err != nil {
		h.logger.Error("failed to update user", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

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

	if err := h.repo.Delete(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to delete user", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

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
		if errors.Is(err, repository.ErrNotFound) {
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
		if errors.Is(err, repository.ErrNotFound) {
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

	if req.DisplayName != nil {
		if *req.DisplayName == "" {
			ErrBadRequest(w, "display_name cannot be empty")
			return
		}
		user.DisplayName = *req.DisplayName
	}
	if req.Password != nil {
		if *req.Password == "" {
			ErrBadRequest(w, "password cannot be empty")
			return
		}
		hashed, err := auth.HashPassword(*req.Password)
		if err != nil {
			h.logger.Error("failed to hash password", zap.Error(err))
			ErrInternal(w)
			return
		}
		user.Password = db.EncryptedString(hashed)
	}

	if err := h.repo.Update(r.Context(), user); err != nil {
		h.logger.Error("failed to update current user", zap.String("id", claims.UserID), zap.Error(err))
		ErrInternal(w)
		return
	}

	Ok(w, userToResponse(user))
}