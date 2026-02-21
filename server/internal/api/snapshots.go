package api

import (
	"errors"
	"net/http"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repository"
)

// SnapshotHandler groups all snapshot-related HTTP handlers.
// Snapshots are created automatically after each successful backup job and
// cached in the database. They are read-only except for deletion, which
// removes the cached record only — pruning the actual data from the backup
// engine is handled separately by the retention policy enforcement.
type SnapshotHandler struct {
	repo   repository.SnapshotRepository
	logger *zap.Logger
}

// NewSnapshotHandler creates a new SnapshotHandler.
func NewSnapshotHandler(repo repository.SnapshotRepository, logger *zap.Logger) *SnapshotHandler {
	return &SnapshotHandler{
		repo:   repo,
		logger: logger.Named("snapshot_handler"),
	}
}

// -----------------------------------------------------------------------------
// Response types
// -----------------------------------------------------------------------------

// snapshotResponse is the JSON representation of a snapshot.
type snapshotResponse struct {
	ID            string `json:"id"`
	PolicyID      string `json:"policy_id"`
	DestinationID string `json:"destination_id"`
	JobID         string `json:"job_id"`
	SnapshotID    string `json:"snapshot_id"` // opaque ID from the backup engine
	SizeBytes     int64  `json:"size_bytes"`
	FileCount     int64  `json:"file_count"`
	Tags          string `json:"tags"`
	SnapshotAt    string `json:"snapshot_at"`
	CreatedAt     string `json:"created_at"`
}

// snapshotToResponse converts a db.Snapshot to a snapshotResponse.
func snapshotToResponse(s *db.Snapshot) snapshotResponse {
	return snapshotResponse{
		ID:            s.ID.String(),
		PolicyID:      s.PolicyID.String(),
		DestinationID: s.DestinationID.String(),
		JobID:         s.JobID.String(),
		SnapshotID:    s.SnapshotID,
		SizeBytes:     s.SizeBytes,
		FileCount:     s.FileCount,
		Tags:          s.Tags,
		SnapshotAt:    s.SnapshotAt.UTC().String(),
		CreatedAt:     s.CreatedAt.UTC().String(),
	}
}

// listSnapshotsResponse wraps a paginated list of snapshots.
type listSnapshotsResponse struct {
	Items []snapshotResponse `json:"items"`
	Total int64              `json:"total"`
}

// -----------------------------------------------------------------------------
// Handlers
// -----------------------------------------------------------------------------

// List handles GET /api/v1/snapshots.
// Supports optional filtering by policy_id or destination_id via query params.
// If both are provided, policy_id takes precedence.
func (h *SnapshotHandler) List(w http.ResponseWriter, r *http.Request) {
	opts := paginationOpts(r)

	if policyID := r.URL.Query().Get("policy_id"); policyID != "" {
		id, err := parseUUIDString(policyID)
		if err != nil {
			ErrBadRequest(w, "invalid policy_id: must be a valid UUID")
			return
		}
		snapshots, total, err := h.repo.ListByPolicy(r.Context(), id, opts)
		if err != nil {
			h.logger.Error("failed to list snapshots by policy", zap.Error(err))
			ErrInternal(w)
			return
		}
		h.writeSnapshotList(w, snapshots, total)
		return
	}

	if destinationID := r.URL.Query().Get("destination_id"); destinationID != "" {
		id, err := parseUUIDString(destinationID)
		if err != nil {
			ErrBadRequest(w, "invalid destination_id: must be a valid UUID")
			return
		}
		snapshots, total, err := h.repo.ListByDestination(r.Context(), id, opts)
		if err != nil {
			h.logger.Error("failed to list snapshots by destination", zap.Error(err))
			ErrInternal(w)
			return
		}
		h.writeSnapshotList(w, snapshots, total)
		return
	}

	snapshots, total, err := h.repo.List(r.Context(), opts)
	if err != nil {
		h.logger.Error("failed to list snapshots", zap.Error(err))
		ErrInternal(w)
		return
	}
	h.writeSnapshotList(w, snapshots, total)
}

// GetByID handles GET /api/v1/snapshots/{id}.
func (h *SnapshotHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	snapshot, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get snapshot", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	Ok(w, snapshotToResponse(snapshot))
}

// Delete handles DELETE /api/v1/snapshots/{id}.
// Removes the cached snapshot record from the database. The actual snapshot
// data in the backup engine is not deleted here — use the retention policy
// enforcement (restic forget/prune) to remove data from the engine.
func (h *SnapshotHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	if err := h.repo.Delete(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to delete snapshot", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	NoContent(w)
}

// -----------------------------------------------------------------------------
// Internal helpers
// -----------------------------------------------------------------------------

// writeSnapshotList converts a slice of db.Snapshot and writes the response.
func (h *SnapshotHandler) writeSnapshotList(w http.ResponseWriter, snapshots []db.Snapshot, total int64) {
	items := make([]snapshotResponse, len(snapshots))
	for i := range snapshots {
		items[i] = snapshotToResponse(&snapshots[i])
	}
	Ok(w, listSnapshotsResponse{Items: items, Total: total})
}