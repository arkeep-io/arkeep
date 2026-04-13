package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// logAuditDirect persists a single audit record when the caller already has
// explicit user identity (e.g. the Login handler where JWT claims are not yet
// in context). Same fire-and-forget semantics as logAudit.
func logAuditDirect(
	r *http.Request,
	repo repositories.AuditRepository,
	logger *zap.Logger,
	userID uuid.UUID,
	userEmail string,
	action string,
	resType string,
	resID string,
	details map[string]any,
) {
	detailsJSON, _ := json.Marshal(details)
	entry := &db.AuditLog{
		UserID:       userID,
		UserEmail:    userEmail,
		Action:       action,
		ResourceType: resType,
		ResourceID:   resID,
		Details:      string(detailsJSON),
		IPAddress:    r.RemoteAddr,
	}
	if err := repo.Create(r.Context(), entry); err != nil {
		logger.Error("audit log write failed",
			zap.String("action", action),
			zap.Error(err),
		)
	}
}

// logAudit persists a single audit record asynchronously. Errors are logged
// but never propagated — an audit write failure must not block the caller.
//
// action     — dot-separated event name, e.g. "policy.update", "snapshot.restore"
// resType    — resource category, e.g. "policy", "snapshot", "user", "settings"
// resID      — UUID string of the affected resource (empty string if N/A)
// details    — arbitrary JSON-serialisable map with non-sensitive context
//
// logAudit is a package-level function so all handler files in the api package
// can call it without any extra wiring — they only need an AuditRepository ref.
func logAudit(
	r *http.Request,
	repo repositories.AuditRepository,
	logger *zap.Logger,
	action string,
	resType string,
	resID string,
	details map[string]any,
) {
	claims := claimsFromCtx(r.Context())
	if claims == nil {
		return
	}

	detailsJSON, _ := json.Marshal(details)

	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		logger.Error("audit: invalid user id in claims",
			zap.String("action", action),
			zap.String("user_id", claims.UserID),
		)
		return
	}

	entry := &db.AuditLog{
		UserID:       userID,
		UserEmail:    claims.Email,
		Action:       action,
		ResourceType: resType,
		ResourceID:   resID,
		Details:      string(detailsJSON),
		IPAddress:    r.RemoteAddr, // already resolved by middleware.RealIP
	}

	if err := repo.Create(r.Context(), entry); err != nil {
		logger.Error("audit log write failed",
			zap.String("action", action),
			zap.Error(err),
		)
	}
}

// -----------------------------------------------------------------------------
// AuditHandler
// -----------------------------------------------------------------------------

// AuditHandler exposes the read-only audit log endpoint for administrators.
type AuditHandler struct {
	repo   repositories.AuditRepository
	logger *zap.Logger
}

// NewAuditHandler creates a new AuditHandler.
func NewAuditHandler(repo repositories.AuditRepository, logger *zap.Logger) *AuditHandler {
	return &AuditHandler{
		repo:   repo,
		logger: logger.Named("audit_handler"),
	}
}

// auditLogResponse is the JSON representation of a single audit log entry.
type auditLogResponse struct {
	ID           string         `json:"id"`
	UserID       string         `json:"user_id"`
	UserEmail    string         `json:"user_email"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Details      map[string]any `json:"details"`
	IPAddress    string         `json:"ip_address"`
	CreatedAt    string         `json:"created_at"`
}

func auditLogToResponse(e *db.AuditLog) auditLogResponse {
	// Best-effort JSON parse of details — on failure fall back to empty map.
	var details map[string]any
	if err := json.Unmarshal([]byte(e.Details), &details); err != nil || details == nil {
		details = map[string]any{}
	}
	return auditLogResponse{
		ID:           e.ID.String(),
		UserID:       e.UserID.String(),
		UserEmail:    e.UserEmail,
		Action:       e.Action,
		ResourceType: e.ResourceType,
		ResourceID:   e.ResourceID,
		Details:      details,
		IPAddress:    e.IPAddress,
		CreatedAt:    e.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// List handles GET /api/v1/admin/audit.
// Supported query parameters:
//
//	limit, offset      — pagination (default limit 50, max 200)
//	user_id            — filter by user UUID
//	action             — prefix filter, e.g. "policy." or exact "snapshot.restore"
//	resource_type      — exact match
//	from, to           — RFC3339 timestamps for created_at range
func (h *AuditHandler) List(w http.ResponseWriter, r *http.Request) {
	opts := paginationOpts(r)

	filter := repositories.AuditFilter{}

	if raw := r.URL.Query().Get("user_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			ErrBadRequest(w, "invalid user_id")
			return
		}
		filter.UserID = &id
	}

	if action := r.URL.Query().Get("action"); action != "" {
		filter.Action = action
	}
	if rt := r.URL.Query().Get("resource_type"); rt != "" {
		filter.ResourceType = rt
	}

	if fromStr := r.URL.Query().Get("from"); fromStr != "" {
		t, err := time.Parse(time.RFC3339, fromStr)
		if err != nil {
			ErrBadRequest(w, "invalid from timestamp, expected RFC3339")
			return
		}
		filter.From = &t
	}
	if toStr := r.URL.Query().Get("to"); toStr != "" {
		t, err := time.Parse(time.RFC3339, toStr)
		if err != nil {
			ErrBadRequest(w, "invalid to timestamp, expected RFC3339")
			return
		}
		filter.To = &t
	}

	entries, total, err := h.repo.List(r.Context(), filter, opts)
	if err != nil {
		h.logger.Error("failed to list audit log", zap.Error(err))
		ErrInternal(w)
		return
	}

	items := make([]auditLogResponse, len(entries))
	for i := range entries {
		items[i] = auditLogToResponse(&entries[i])
	}

	Ok(w, map[string]any{
		"items": items,
		"total": total,
	})
}
