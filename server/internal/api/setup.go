package api

import (
	"net/http"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// setupHandler handles the public setup endpoints used during first-time
// server initialisation. Both routes bypass JWT authentication because no
// users exist yet when they are called.
type setupHandler struct {
	users  repositories.UserRepository
	logger *zap.Logger
}

// NewSetupHandler constructs a setupHandler with the given dependencies.
func NewSetupHandler(users repositories.UserRepository, logger *zap.Logger) *setupHandler {
	return &setupHandler{users: users, logger: logger.Named("setup")}
}

// setupStatusResponse is the payload returned by GET /api/v1/setup/status.
type setupStatusResponse struct {
	// Completed is true when at least one user exists in the database,
	// meaning the initial admin account has already been created.
	Completed bool `json:"completed"`
}

// setupCompleteRequest is the body accepted by POST /api/v1/setup/complete.
type setupCompleteRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// GetStatus reports whether the initial setup has been completed.
// Setup is considered complete when at least one user exists in the database.
// This endpoint is intentionally public — the frontend calls it on first load
// to decide whether to show the setup page or the login page.
func (h *setupHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	_, total, err := h.users.List(r.Context(), repositories.ListOptions{Limit: 1})
	if err != nil {
		h.logger.Error("setup: failed to count users", zap.Error(err))
		ErrInternal(w)
		return
	}

	Ok(w, setupStatusResponse{Completed: total > 0})
}

// Complete creates the first admin user and marks setup as done.
// Returns 409 Conflict if any user already exists, preventing this public
// endpoint from being used as an unauthenticated account-creation backdoor
// after the initial setup is done.
func (h *setupHandler) Complete(w http.ResponseWriter, r *http.Request) {
	var req setupCompleteRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" || req.Email == "" || req.Password == "" {
		ErrBadRequest(w, "name, email, and password are required")
		return
	}

	// Guard: refuse if any user already exists. Re-checking here (rather than
	// relying solely on the frontend having checked /setup/status first) closes
	// the TOCTOU window and makes the endpoint safe to call directly.
	_, total, err := h.users.List(r.Context(), repositories.ListOptions{Limit: 1})
	if err != nil {
		h.logger.Error("setup: failed to count users", zap.Error(err))
		ErrInternal(w)
		return
	}
	if total > 0 {
		ErrConflict(w, "setup already completed")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		h.logger.Error("setup: failed to hash password", zap.Error(err))
		ErrInternal(w)
		return
	}

	user := &db.User{
		DisplayName: req.Name,
		Email:       req.Email,
		Password:    db.EncryptedString(hash),
		Role:        "admin",
	}

	if err := h.users.Create(r.Context(), user); err != nil {
		h.logger.Error("setup: failed to create admin user", zap.Error(err))
		ErrInternal(w)
		return
	}

	h.logger.Info("setup completed: first admin user created",
		zap.String("user_id", user.ID.String()),
		zap.String("email", user.Email),
	)

	Created(w, map[string]string{"message": "setup completed"})
}