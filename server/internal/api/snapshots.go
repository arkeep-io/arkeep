package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/destutil"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// SnapshotHandler groups all snapshot-related HTTP handlers.
// Snapshots are created automatically after each successful backup job and
// cached in the database. They are read-only except for deletion, which
// removes the cached record only — pruning the actual data from the backup
// engine is handled separately by the retention policy enforcement.
type SnapshotHandler struct {
	repo      repositories.SnapshotRepository
	dests     repositories.DestinationRepository
	policies  repositories.PolicyRepository
	jobs      repositories.JobRepository
	agentMgr  *agentmanager.Manager
	auditRepo repositories.AuditRepository
	logger    *zap.Logger
}

// NewSnapshotHandler creates a new SnapshotHandler.
func NewSnapshotHandler(
	repo repositories.SnapshotRepository,
	dests repositories.DestinationRepository,
	policies repositories.PolicyRepository,
	jobs repositories.JobRepository,
	agentMgr *agentmanager.Manager,
	auditRepo repositories.AuditRepository,
	logger *zap.Logger,
) *SnapshotHandler {
	return &SnapshotHandler{
		repo:      repo,
		dests:     dests,
		policies:  policies,
		jobs:      jobs,
		agentMgr:  agentMgr,
		auditRepo: auditRepo,
		logger:    logger.Named("snapshot_handler"),
	}
}

// -----------------------------------------------------------------------------
// Request / Response types
// -----------------------------------------------------------------------------

// snapshotResponse is the JSON representation of a snapshot returned by the API.
type snapshotResponse struct {
	ID               string `json:"id"`
	PolicyID         string `json:"policy_id"`
	PolicyName       string `json:"policy_name"`
	DestinationID    string `json:"destination_id"`
	DestinationName  string `json:"destination_name"`
	JobID            string `json:"job_id"`
	ResticSnapshotID string `json:"restic_snapshot_id"`
	SizeBytes        int64  `json:"size_bytes"`
	Tags             string `json:"tags"`
	CreatedAt        string `json:"created_at"`
}

// listSnapshotsResponse wraps a paginated list of snapshots.
type listSnapshotsResponse struct {
	Items []snapshotResponse `json:"items"`
	Total int64              `json:"total"`
}

// restoreRequest is the body for POST /api/v1/snapshots/{id}/restore.
type restoreRequest struct {
	AgentID    string `json:"agent_id"`
	TargetPath string `json:"target_path"`
}

// restoreResponse is returned after a restore job is successfully dispatched.
type restoreResponse struct {
	JobID string `json:"job_id"`
}

// restorePayload is the JSON-encoded payload embedded in a JobAssignment
// for JOB_TYPE_RESTORE jobs. Mirrors the struct in the agent executor.
type restorePayload struct {
	ResticSnapshotID string            `json:"restic_snapshot_id"`
	RepoPassword     string            `json:"repo_password"`
	TargetPath       string            `json:"target_path"`
	Destination      destinationFields `json:"destination"`
}

// destinationFields carries the resolved details of the backup destination
// needed by the agent to authenticate against the restic repository.
type destinationFields struct {
	DestinationID string            `json:"destination_id"`
	Type          string            `json:"type"`
	RepoURL       string            `json:"repo_url"`
	Env           map[string]string `json:"env"`
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

	logAudit(r, h.auditRepo, h.logger, "snapshot.delete", "snapshot", id.String(), map[string]any{})
	NoContent(w)
}

// Restore handles POST /api/v1/snapshots/{id}/restore.
// Creates a restore job and dispatches it to the chosen agent via gRPC.
// The agent will run `restic restore <snapshot_id> --target <target_path>`.
//
// Flow:
//  1. Load snapshot → get restic_snapshot_id, destination_id, policy_id
//  2. Load destination → build repo URL and env (credentials)
//  3. Load policy → get repo password
//  4. Create db.Job{Type: "restore"} for the chosen agent
//  5. Build and dispatch JobAssignment with JOB_TYPE_RESTORE
func (h *SnapshotHandler) Restore(w http.ResponseWriter, r *http.Request) {
	snapshotID, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	var req restoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ErrBadRequest(w, "invalid request body")
		return
	}
	if req.AgentID == "" {
		ErrBadRequest(w, "agent_id is required")
		return
	}
	if req.TargetPath == "" {
		ErrBadRequest(w, "target_path is required")
		return
	}

	agentID, err := uuid.Parse(req.AgentID)
	if err != nil {
		ErrBadRequest(w, "invalid agent_id: must be a valid UUID")
		return
	}

	ctx := r.Context()

	// --- 1. Load snapshot ---
	snapshot, err := h.repo.GetByID(ctx, snapshotID)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to load snapshot for restore", zap.Error(err))
		ErrInternal(w)
		return
	}

	// --- 2. Load destination ---
	dest, err := h.dests.GetByID(ctx, snapshot.DestinationID)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrBadRequest(w, "destination not found")
			return
		}
		h.logger.Error("failed to load destination for restore", zap.Error(err))
		ErrInternal(w)
		return
	}

	// --- 3. Load policy (for repo password) ---
	policy, err := h.policies.GetByID(ctx, snapshot.PolicyID)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrBadRequest(w, "policy not found")
			return
		}
		h.logger.Error("failed to load policy for restore", zap.Error(err))
		ErrInternal(w)
		return
	}

	// --- 4. Create restore job ---
	job := &db.Job{
		PolicyID: snapshot.PolicyID,
		AgentID:  agentID,
		Type:     "restore",
		Status:   "pending",
	}
	if err := h.jobs.Create(ctx, job); err != nil {
		h.logger.Error("failed to create restore job", zap.Error(err))
		ErrInternal(w)
		return
	}

	// --- 5. Build and dispatch ---
	payload := restorePayload{
		ResticSnapshotID: snapshot.SnapshotID,
		RepoPassword:     string(policy.RepoPassword),
		TargetPath:       req.TargetPath,
		Destination: destinationFields{
			DestinationID: dest.ID.String(),
			Type:          dest.Type,
			RepoURL:       destutil.BuildRepoURL(dest),
			Env:           destutil.BuildEnv(dest),
		},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		h.logger.Error("failed to marshal restore payload", zap.Error(err))
		ErrInternal(w)
		return
	}

	assignment := &proto.JobAssignment{
		JobId:       job.ID.String(),
		PolicyId:    job.PolicyID.String(),
		Type:        proto.JobType_JOB_TYPE_RESTORE,
		Payload:     payloadBytes,
		ScheduledAt: timestamppb.Now(),
	}

	if err := h.agentMgr.Dispatch(agentID.String(), assignment); err != nil {
		// Job is persisted as pending — it will remain in the DB but won't
		// be retried automatically (restore jobs are user-initiated, not
		// scheduled). Log the error and return 503 so the GUI can retry.
		h.logger.Warn("failed to dispatch restore job, agent may be offline",
			zap.String("job_id", job.ID.String()),
			zap.String("agent_id", agentID.String()),
			zap.Error(err),
		)
		ErrServiceUnavailable(w, "agent is not connected — ensure the agent is online and try again")
		return
	}

	h.logger.Info("restore job dispatched",
		zap.String("job_id", job.ID.String()),
		zap.String("snapshot_id", snapshot.SnapshotID),
		zap.String("agent_id", agentID.String()),
		zap.String("target_path", req.TargetPath),
	)

	logAudit(r, h.auditRepo, h.logger, "snapshot.restore", "snapshot", snapshotID.String(), map[string]any{
		"snapshot_id":    snapshot.SnapshotID,
		"destination_id": snapshot.DestinationID.String(),
		"target_path":    req.TargetPath,
		"agent_id":       agentID.String(),
	})
	Ok(w, restoreResponse{JobID: job.ID.String()})
}

// -----------------------------------------------------------------------------
// Internal helpers
// -----------------------------------------------------------------------------

func (h *SnapshotHandler) writeSnapshotList(w http.ResponseWriter, snapshots []repositories.SnapshotWithNames, total int64) {
	items := make([]snapshotResponse, len(snapshots))
	for i := range snapshots {
		items[i] = snapshotWithNamesToResponse(snapshots[i])
	}
	Ok(w, listSnapshotsResponse{Items: items, Total: total})
}