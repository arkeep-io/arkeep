package api

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

const (
	// apiTokenNameMaxLen caps a token's name, in characters.
	apiTokenNameMaxLen = 100
	// apiTokenMaxPerUser caps how many tokens one user can hold.
	apiTokenMaxPerUser = 50
	// apiTokenMaxExpiryDays caps a token's lifetime when it has one.
	apiTokenMaxExpiryDays = 3650
)

// APITokenHandler manages API tokens (ADR 0027): every user manages their
// own, and admins can list and revoke everyone's. All its routes require a
// session (RequireSession): an API token cannot manage tokens.
type APITokenHandler struct {
	tokens    repositories.APITokenRepository
	users     repositories.UserRepository
	auditRepo repositories.AuditRepository
	logger    *zap.Logger
	now       func() time.Time
}

// NewAPITokenHandler creates a new APITokenHandler.
func NewAPITokenHandler(
	tokens repositories.APITokenRepository,
	users repositories.UserRepository,
	auditRepo repositories.AuditRepository,
	logger *zap.Logger,
) *APITokenHandler {
	return &APITokenHandler{
		tokens:    tokens,
		users:     users,
		auditRepo: auditRepo,
		logger:    logger.Named("api_token_handler"),
		now:       time.Now,
	}
}

// apiTokenResponse is an API token as listed. The token itself is never
// returned after its creation.
type apiTokenResponse struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Hint       string  `json:"hint"`
	Scope      string  `json:"scope"`
	ExpiresAt  *string `json:"expires_at"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
	// UserID and UserEmail are set in the admin listing only.
	UserID    string `json:"user_id,omitempty"`
	UserEmail string `json:"user_email,omitempty"`
}

// createdAPITokenResponse is returned once, at creation: it is the only time
// the token can be read.
type createdAPITokenResponse struct {
	apiTokenResponse
	Token string `json:"token"`
}

func formatOptionalTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func apiTokenToResponse(t *db.APIToken) apiTokenResponse {
	return apiTokenResponse{
		ID:         t.ID.String(),
		Name:       t.Name,
		Hint:       t.Hint,
		Scope:      t.Scope,
		ExpiresAt:  formatOptionalTime(t.ExpiresAt),
		LastUsedAt: formatOptionalTime(t.LastUsedAt),
		CreatedAt:  t.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// currentUserID returns the authenticated user's ID, or writes an error
// response and reports ok=false.
func (h *APITokenHandler) currentUserID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	claims := claimsFromCtx(r.Context())
	if claims == nil {
		ErrUnauthorized(w)
		return uuid.UUID{}, false
	}
	id, err := parseUUIDString(claims.UserID)
	if err != nil {
		ErrUnauthorized(w)
		return uuid.UUID{}, false
	}
	return id, true
}

// ListMine handles GET /api/v1/users/me/api-tokens.
func (h *APITokenHandler) ListMine(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	tokens, err := h.tokens.ListByUser(r.Context(), userID)
	if err != nil {
		h.logger.Error("failed to list api tokens", zap.Error(err))
		ErrInternal(w)
		return
	}
	out := make([]apiTokenResponse, 0, len(tokens))
	for i := range tokens {
		out = append(out, apiTokenToResponse(&tokens[i]))
	}
	Ok(w, out)
}

// createAPITokenRequest is the body of POST /api/v1/users/me/api-tokens.
type createAPITokenRequest struct {
	Name string `json:"name"`
	// ExpiresInDays is the token's lifetime, from 1 to 3650 days. ExpiresAt
	// is its expiry as a date and time (RFC 3339), in the future and at most
	// 3650 days ahead. At most one of the two is set: with neither, the token
	// does not expire.
	ExpiresInDays *int    `json:"expires_in_days"`
	ExpiresAt     *string `json:"expires_at"`
}

// CreateMine handles POST /api/v1/users/me/api-tokens. The response holds
// the token, which cannot be read again.
func (h *APITokenHandler) CreateMine(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	var req createAPITokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		ErrUnprocessable(w, "name is required")
		return
	}
	if utf8.RuneCountInString(name) > apiTokenNameMaxLen {
		ErrUnprocessable(w, "name must be at most 100 characters")
		return
	}
	now := h.now()
	maxExpiry := now.Add(apiTokenMaxExpiryDays * 24 * time.Hour)
	var expiresAt *time.Time
	switch {
	case req.ExpiresInDays != nil && req.ExpiresAt != nil:
		ErrUnprocessable(w, "set expires_in_days or expires_at, not both")
		return
	case req.ExpiresInDays != nil:
		days := *req.ExpiresInDays
		if days < 1 || days > apiTokenMaxExpiryDays {
			ErrUnprocessable(w, "expires_in_days must be between 1 and 3650, or null for no expiry")
			return
		}
		t := now.Add(time.Duration(days) * 24 * time.Hour).UTC()
		expiresAt = &t
	case req.ExpiresAt != nil:
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			ErrUnprocessable(w, "expires_at must be an RFC 3339 date and time")
			return
		}
		if !t.After(now) || t.After(maxExpiry) {
			ErrUnprocessable(w, "expires_at must be in the future and at most 3650 days ahead")
			return
		}
		t = t.UTC()
		expiresAt = &t
	}

	user, err := h.users.GetByID(r.Context(), userID)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrUnauthorized(w)
			return
		}
		h.logger.Error("failed to load user", zap.Error(err))
		ErrInternal(w)
		return
	}
	if !user.IsActive {
		ErrForbidden(w)
		return
	}

	count, err := h.tokens.CountByUser(r.Context(), userID)
	if err != nil {
		h.logger.Error("failed to count api tokens", zap.Error(err))
		ErrInternal(w)
		return
	}
	if count >= apiTokenMaxPerUser {
		ErrConflict(w, "too many API tokens: revoke one you no longer use")
		return
	}

	raw, hash, hint, err := auth.GenerateAPIToken()
	if err != nil {
		h.logger.Error("failed to generate api token", zap.Error(err))
		ErrInternal(w)
		return
	}
	token := &db.APIToken{
		UserID:    userID,
		Name:      name,
		TokenHash: hash,
		Hint:      hint,
		Scope:     auth.APITokenScopeRead,
		ExpiresAt: expiresAt,
	}
	if err := h.tokens.Create(r.Context(), token); err != nil {
		h.logger.Error("failed to create api token", zap.Error(err))
		ErrInternal(w)
		return
	}

	logAudit(r, h.auditRepo, h.logger, "api_token.create", "api_token", token.ID.String(), map[string]any{
		"name":       token.Name,
		"scope":      token.Scope,
		"expires_at": formatOptionalTime(token.ExpiresAt),
	})
	Created(w, createdAPITokenResponse{apiTokenResponse: apiTokenToResponse(token), Token: raw})
}

// RevokeMine handles DELETE /api/v1/users/me/api-tokens/{id}. A token of
// another user answers 404, as if it did not exist.
func (h *APITokenHandler) RevokeMine(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	h.revoke(w, r, id, &userID)
}

// ListAll handles GET /api/v1/api-tokens (admin): every user's tokens.
func (h *APITokenHandler) ListAll(w http.ResponseWriter, r *http.Request) {
	rows, err := h.tokens.ListAll(r.Context())
	if err != nil {
		h.logger.Error("failed to list api tokens", zap.Error(err))
		ErrInternal(w)
		return
	}
	out := make([]apiTokenResponse, 0, len(rows))
	for i := range rows {
		resp := apiTokenToResponse(&rows[i].APIToken)
		resp.UserID = rows[i].UserID.String()
		resp.UserEmail = rows[i].UserEmail
		out = append(out, resp)
	}
	Ok(w, out)
}

// Revoke handles DELETE /api/v1/api-tokens/{id} (admin): revokes any user's
// token.
func (h *APITokenHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	h.revoke(w, r, id, nil)
}

func (h *APITokenHandler) revoke(w http.ResponseWriter, r *http.Request, id uuid.UUID, owner *uuid.UUID) {
	token, err := h.tokens.Delete(r.Context(), id, owner)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to revoke api token", zap.Error(err))
		ErrInternal(w)
		return
	}
	logAudit(r, h.auditRepo, h.logger, "api_token.revoke", "api_token", token.ID.String(), map[string]any{
		"name":    token.Name,
		"user_id": token.UserID.String(),
	})
	NoContent(w)
}
