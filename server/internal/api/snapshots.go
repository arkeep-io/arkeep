package api

import (
	"errors"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// SnapshotHandler groups all snapshot-related HTTP handlers.
// Snapshots are created automatically after each successful backup job and
// cached in the database. They are read-only except for deletion, which
// removes the cached record only — pruning the actual data from the backup
// engine is handled separately by the retention policy enforcement.
type SnapshotHandler struct {
	repo   repositories.SnapshotRepository
	logger *zap.Logger
}

// NewSnapshotHandler creates a new SnapshotHandler.
func NewSnapshotHandler(repo repositories.SnapshotRepository, logger *zap.Logger) *SnapshotHandler {
	return &SnapshotHandler{
		repo:   repo,
		logger: logger.Named("snapshot_handler"),
	}
}

// -----------------------------------------------------------------------------
// Response types
// -----------------------------------------------------------------------------

// snapshotResponse is the JSON representation of a snapshot returned by the API.
// Field names match the TypeScript Snapshot interface in gui/src/types/index.ts.
//
// Key naming decisions:
//   - restic_snapshot_id: the opaque hash returned by restic (not the internal UUID)
//   - policy_name / destination_name: denormalised via JOIN for display without extra requests
//   - created_at: the GUI sorts and displays by this field; SnapshotAt is aliased here
type snapshotResponse struct {
	ID              string `json:"id"`
	PolicyID        string `json:"policy_id"`
	PolicyName      string `json:"policy_name"`
	DestinationID   string `json:"destination_id"`
	DestinationName string `json:"destination_name"`
	JobID           string `json:"job_id"`
	ResticSnapshotID string `json:"restic_snapshot_id"` // matches Snapshot.restic_snapshot_id in types/index.ts
	SizeBytes       int64  `json:"size_bytes"`
	Tags            string `json:"tags"`
	CreatedAt       string `json:"created_at"` // mapped from SnapshotAt for frontend compatibility
}

// listSnapshotsResponse wraps a paginated list of snapshots.
type listSnapshotsResponse struct {
	Items []snapshotResponse `json:"items"`
	Total int64              `json:"total"`
}

// snapshotWithNamesToResponse converts a SnapshotWithNames to a snapshotResponse.
func snapshotWithNamesToResponse(s repositories.SnapshotWithNames) snapshotResponse {
	return snapshotResponse{
		ID:               s.ID.String(),
		PolicyID:         s.PolicyID.String(),
		PolicyName:       s.PolicyName,
		DestinationID:    s.DestinationID.String(),
		DestinationName:  s.DestinationName,
		JobID:            s.JobID.String(),
		ResticSnapshotID: s.SnapshotID,
		SizeBytes:        s.SizeBytes,
		Tags:             s.Tags,
		CreatedAt:        s.SnapshotAt.UTC().Format(time.RFC3339),
	}
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
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get snapshot", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	// GetByID returns a plain db.Snapshot without names — wrap it minimally.
	// Names are only needed for list views; the detail endpoint is used
	// for single-snapshot operations where the caller already has context.
	Ok(w, snapshotResponse{
		ID:               snapshot.ID.String(),
		PolicyID:         snapshot.PolicyID.String(),
		DestinationID:    snapshot.DestinationID.String(),
		JobID:            snapshot.JobID.String(),
		ResticSnapshotID: snapshot.SnapshotID,
		SizeBytes:        snapshot.SizeBytes,
		Tags:             snapshot.Tags,
		CreatedAt:        snapshot.SnapshotAt.UTC().Format(time.RFC3339),
	})
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
		if errors.Is(err, repositories.ErrNotFound) {
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

// writeSnapshotList converts a slice of SnapshotWithNames and writes the paginated response.
func (h *SnapshotHandler) writeSnapshotList(w http.ResponseWriter, snapshots []repositories.SnapshotWithNames, total int64) {
	items := make([]snapshotResponse, len(snapshots))
	for i := range snapshots {
		items[i] = snapshotWithNamesToResponse(snapshots[i])
	}
	Ok(w, listSnapshotsResponse{Items: items, Total: total})
}