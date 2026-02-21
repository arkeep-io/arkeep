package api

import (
	"errors"
	"net/http"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// DestinationHandler groups all destination-related HTTP handlers.
type DestinationHandler struct {
	repo   repositories.DestinationRepository
	logger *zap.Logger
}

// NewDestinationHandler creates a new DestinationHandler.
func NewDestinationHandler(repo repositories.DestinationRepository, logger *zap.Logger) *DestinationHandler {
	return &DestinationHandler{
		repo:   repo,
		logger: logger.Named("destination_handler"),
	}
}

// destinationResponse is the JSON representation of a destination.
// Credentials are intentionally omitted from all responses — they are
// write-only and never returned to the client after creation.
type destinationResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Config    string `json:"config"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// destinationToResponse converts a db.Destination to a destinationResponse.
func destinationToResponse(d *db.Destination) destinationResponse {
	return destinationResponse{
		ID:        d.ID.String(),
		Name:      d.Name,
		Type:      d.Type,
		Config:    d.Config,
		Enabled:   d.Enabled,
		CreatedAt: d.CreatedAt.UTC().String(),
		UpdatedAt: d.UpdatedAt.UTC().String(),
	}
}

// listDestinationsResponse wraps a paginated list of destinations.
type listDestinationsResponse struct {
	Items []destinationResponse `json:"items"`
	Total int64                 `json:"total"`
}

// validDestinationTypes lists the accepted destination type values.
var validDestinationTypes = map[string]bool{
	"local":  true,
	"s3":     true,
	"sftp":   true,
	"rest":   true,
	"rclone": true,
}

// List handles GET /api/v1/destinations.
func (h *DestinationHandler) List(w http.ResponseWriter, r *http.Request) {
	opts := paginationOpts(r)

	destinations, total, err := h.repo.List(r.Context(), opts)
	if err != nil {
		h.logger.Error("failed to list destinations", zap.Error(err))
		ErrInternal(w)
		return
	}

	items := make([]destinationResponse, len(destinations))
	for i := range destinations {
		items[i] = destinationToResponse(&destinations[i])
	}

	Ok(w, listDestinationsResponse{Items: items, Total: total})
}

// createDestinationRequest is the JSON body expected by POST /api/v1/destinations.
// Credentials is a JSON string containing provider-specific auth data
// (e.g. access key + secret for S3). It is encrypted at rest automatically
// by EncryptedString — the handler stores it as plain text and the DB layer
// handles encryption transparently.
type createDestinationRequest struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Credentials string `json:"credentials"` // JSON, stored encrypted
	Config      string `json:"config"`      // JSON, not sensitive
}

// Create handles POST /api/v1/destinations.
func (h *DestinationHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createDestinationRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		ErrBadRequest(w, "name is required")
		return
	}
	if !validDestinationTypes[req.Type] {
		ErrBadRequest(w, "type must be one of: local, s3, sftp, rest, rclone")
		return
	}
	if req.Config == "" {
		req.Config = "{}"
	}

	dest := &db.Destination{
		Name:        req.Name,
		Type:        req.Type,
		Credentials: db.EncryptedString(req.Credentials),
		Config:      req.Config,
		Enabled:     true,
	}

	if err := h.repo.Create(r.Context(), dest); err != nil {
		h.logger.Error("failed to create destination", zap.Error(err))
		ErrInternal(w)
		return
	}

	Created(w, destinationToResponse(dest))
}

// GetByID handles GET /api/v1/destinations/{id}.
func (h *DestinationHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	dest, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get destination", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	Ok(w, destinationToResponse(dest))
}

// updateDestinationRequest is the JSON body for PATCH /api/v1/destinations/{id}.
// All fields are optional — only non-nil values are applied.
type updateDestinationRequest struct {
	Name        *string `json:"name"`
	Credentials *string `json:"credentials"`
	Config      *string `json:"config"`
	Enabled     *bool   `json:"enabled"`
}

// Update handles PATCH /api/v1/destinations/{id}.
func (h *DestinationHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	var req updateDestinationRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	dest, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get destination for update", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	if req.Name != nil {
		if *req.Name == "" {
			ErrBadRequest(w, "name cannot be empty")
			return
		}
		dest.Name = *req.Name
	}
	if req.Credentials != nil {
		dest.Credentials = db.EncryptedString(*req.Credentials)
	}
	if req.Config != nil {
		dest.Config = *req.Config
	}
	if req.Enabled != nil {
		dest.Enabled = *req.Enabled
	}

	if err := h.repo.Update(r.Context(), dest); err != nil {
		h.logger.Error("failed to update destination", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	Ok(w, destinationToResponse(dest))
}

// Delete handles DELETE /api/v1/destinations/{id}.
// Returns 409 if the destination is still referenced by an active policy.
func (h *DestinationHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	if err := h.repo.Delete(r.Context(), id); err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		// A foreign key constraint violation means the destination is still
		// referenced by one or more policies. Surface this as a 409.
		h.logger.Warn("failed to delete destination", zap.String("id", id.String()), zap.Error(err))
		ErrConflict(w, "destination is still referenced by one or more policies")
		return
	}

	NoContent(w)
}