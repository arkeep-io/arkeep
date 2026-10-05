package api

import (
	"context"
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
	// tickets holds the pending download tickets: see CreateDownload.
	tickets *downloadTickets
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
		tickets:   newDownloadTickets(),
	}
}

// -----------------------------------------------------------------------------
// Request / Response types
// -----------------------------------------------------------------------------

// snapshotResponse is the JSON representation of a snapshot returned by the API.
type snapshotResponse struct {
	ID              string `json:"id"`
	PolicyID        string `json:"policy_id"`
	PolicyName      string `json:"policy_name"`
	DestinationID   string `json:"destination_id"`
	DestinationName string `json:"destination_name"`
	DestinationType string `json:"destination_type"`
	// DestinationDeleted is true when the snapshot's destination was deleted.
	// Its stored credentials are wiped on delete, so a restore needs them
	// supplied again and browse/download are unavailable.
	DestinationDeleted bool `json:"destination_deleted"`
	// RepoPasswordRequired is true when a restore must also supply the restic
	// repository password: the destination is deleted and no live policy holds
	// the password (the policy is gone, or the snapshot was imported).
	RepoPasswordRequired bool   `json:"repo_password_required"`
	AgentID              string `json:"agent_id"`
	AgentName            string `json:"agent_name"`
	JobID                string `json:"job_id"`
	ResticSnapshotID     string `json:"restic_snapshot_id"`
	SizeBytes            int64  `json:"size_bytes"`
	Tags                 string `json:"tags"`
	Hostname             string `json:"hostname"`
	IsImported           bool   `json:"is_imported"`
	CreatedAt            string `json:"created_at"`
}

// listSnapshotsResponse wraps a paginated list of snapshots.
type listSnapshotsResponse struct {
	Items []snapshotResponse `json:"items"`
	Total int64              `json:"total"`
}

// restoreRequest is the body for POST /api/v1/snapshots/{id}/restore.
type restoreRequest struct {
	AgentID      string   `json:"agent_id"`
	TargetPath   string   `json:"target_path"`
	IncludePaths []string `json:"include_paths,omitempty"`
	// Credentials and RepoPassword are only read when the snapshot's
	// destination was deleted (its stored secrets are wiped on delete). They
	// are used for this one restore and never persisted.
	Credentials  map[string]string `json:"credentials,omitempty"`
	RepoPassword string            `json:"repo_password,omitempty"`
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
	IncludePaths     []string          `json:"include_paths,omitempty"`
	Destination      destinationFields `json:"destination"`
}

// snapshotRepoAccess is the part of the browse and download payloads that
// tells the agent how to open the snapshot's repository. Mirrors
// snapshotAccess in the agent's connection manager.
type snapshotRepoAccess struct {
	ResticSnapshotID string            `json:"restic_snapshot_id"`
	RepoPassword     string            `json:"repo_password"`
	Destination      destinationFields `json:"destination"`
}

// snapshotBrowsePayload is the JSON-encoded payload for JOB_TYPE_LIST_SNAPSHOT_FILES.
// Sent inline (not persisted) via the StreamJobs stream as a correlation request.
type snapshotBrowsePayload struct {
	snapshotRepoAccess
	Path string `json:"path"` // directory to list; empty means the snapshot root
}

// snapshotFileEntryResponse is a single file or directory within a snapshot.
type snapshotFileEntryResponse struct {
	Path  string `json:"path"`
	Type  string `json:"type"`
	Size  int64  `json:"size"`
	Mtime string `json:"mtime"`
}

// snapshotBrowseResponse is the body returned by GET /api/v1/snapshots/{id}/browse.
type snapshotBrowseResponse struct {
	Entries []snapshotFileEntryResponse `json:"entries"`
}

// destinationFields carries the resolved details of the backup destination
// needed by the agent to authenticate against the restic repository.
type destinationFields struct {
	DestinationID string            `json:"destination_id"`
	Type          string            `json:"type"`
	RepoURL       string            `json:"repo_url"`
	Env           map[string]string `json:"env"`
}

// uuidString renders an optional UUID, returning "" when it is unset.
// Imported snapshots carry no policy and no job.
func uuidString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// snapshotWithNamesToResponse converts a SnapshotWithNames to a snapshotResponse.
func snapshotWithNamesToResponse(s repositories.SnapshotWithNames) snapshotResponse {
	policyID := uuidString(s.PolicyID)
	policyName := s.PolicyName
	jobID := uuidString(s.JobID)
	if s.IsImported {
		policyID = ""
		policyName = "(imported)"
		jobID = ""
	}
	return snapshotResponse{
		ID:                   s.ID.String(),
		PolicyID:             policyID,
		PolicyName:           policyName,
		DestinationID:        s.DestinationID.String(),
		DestinationName:      s.DestinationName,
		DestinationType:      s.DestinationType,
		DestinationDeleted:   s.DestinationDeleted,
		RepoPasswordRequired: s.DestinationDeleted && s.PolicyDeleted,
		AgentID:              s.AgentID,
		AgentName:            s.AgentName,
		JobID:                jobID,
		ResticSnapshotID:     s.SnapshotID,
		SizeBytes:            s.SizeBytes,
		Tags:                 s.Tags,
		Hostname:             s.Hostname,
		IsImported:           s.IsImported,
		CreatedAt:            s.SnapshotAt.UTC().Format(time.RFC3339),
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

	snapshot, err := h.repo.GetByIDWithNames(r.Context(), id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get snapshot", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	Ok(w, snapshotWithNamesToResponse(*snapshot))
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

// Sentinel errors returned by resolveRepoAccess.
var (
	// errRepoPasswordUnavailable means the snapshot was imported and its
	// destination carries no stored repository password, so the repository
	// cannot be opened at all.
	errRepoPasswordUnavailable = errors.New("repository password unavailable")
	// errPolicyMissing means a snapshot produced by a backup no longer has the
	// policy it was created by.
	errPolicyMissing = errors.New("policy not found")
)

// resolveRepoAccess returns the Restic repository password needed to open the
// snapshot's repository and, when the snapshot belongs to a policy, that
// policy's agent.
//
// Snapshots produced by a backup take the password from their policy. Snapshots
// imported from a pre-existing repository have no policy: the password comes
// from the destination (stored at import time) and the returned agent is empty,
// so the caller must supply one.
func (h *SnapshotHandler) resolveRepoAccess(ctx context.Context, snapshot *db.Snapshot, dest *db.Destination) (repoPassword, policyAgentID string, err error) {
	if snapshot.PolicyID == nil {
		if dest.RepoPassword == "" {
			return "", "", errRepoPasswordUnavailable
		}
		return string(dest.RepoPassword), "", nil
	}

	policy, err := h.policies.GetByID(ctx, *snapshot.PolicyID)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			return "", "", errPolicyMissing
		}
		return "", "", err
	}
	return string(policy.RepoPassword), policy.AgentID.String(), nil
}

// writeRepoAccessError maps a resolveRepoAccess failure onto an HTTP response.
func (h *SnapshotHandler) writeRepoAccessError(w http.ResponseWriter, err error, action string) {
	switch {
	case errors.Is(err, errRepoPasswordUnavailable):
		ErrUnprocessable(w, "this snapshot was imported and its destination has no stored repository password — run the import again on the destination to store it")
	case errors.Is(err, errPolicyMissing):
		ErrBadRequest(w, "policy not found")
	default:
		h.logger.Error("failed to resolve repository access",
			zap.String("action", action),
			zap.Error(err),
		)
		ErrInternal(w)
	}
}

// applySuppliedCredentials prepares a deleted destination for a one-off
// restore: it rejects what such a restore cannot do and puts the
// caller-supplied credentials on the in-memory destination (never saved), so
// destutil builds the agent env exactly as for a live destination. It writes
// the error response and returns false on failure.
func applySuppliedCredentials(w http.ResponseWriter, dest *db.Destination, req *restoreRequest) bool {
	if len(req.IncludePaths) > 0 {
		ErrUnprocessable(w, "partial restore is not available for a snapshot whose destination was deleted — restore the whole snapshot instead")
		return false
	}
	if dest.Type == "s3" && (req.Credentials["access_key"] == "" || req.Credentials["secret_key"] == "") {
		ErrUnprocessable(w, "the destination of this snapshot was deleted and its credentials were erased — enter the access key and secret key to restore")
		return false
	}
	dest.Credentials = ""
	if len(req.Credentials) > 0 {
		creds, err := json.Marshal(req.Credentials)
		if err != nil {
			ErrBadRequest(w, "invalid credentials")
			return false
		}
		dest.Credentials = db.EncryptedString(creds)
	}
	return true
}

// failUndispatchedRestore closes a restore job that was created but never
// reached its agent, so it does not linger as pending.
func (h *SnapshotHandler) failUndispatchedRestore(ctx context.Context, jobID uuid.UUID, reason string) {
	now := time.Now()
	if err := h.jobs.UpdateStatus(ctx, jobID, "failed", nil, &now, reason); err != nil {
		h.logger.Warn("failed to mark undispatched restore job as failed",
			zap.String("job_id", jobID.String()),
			zap.Error(err),
		)
	}
}

// Restore handles POST /api/v1/snapshots/{id}/restore.
// Creates a restore job and dispatches it to the chosen agent via gRPC.
// The agent will run `restic restore <snapshot_id> --target <target_path>`.
//
// Flow:
//  1. Load snapshot → get restic_snapshot_id, destination_id, policy_id
//  2. Load destination → build repo URL and env (credentials). A deleted
//     destination's credentials were wiped: the caller supplies them, and the
//     restore must be full
//  3. Resolve repo password (from the policy, or the destination if imported;
//     for a deleted destination with neither, from the caller)
//  4. Require the chosen agent to be online — a restore is never queued
//  5. Create db.Job{Type: "restore"} for the chosen agent
//  6. Build and dispatch JobAssignment with JOB_TYPE_RESTORE; if dispatch
//     still fails, the job is marked failed rather than left pending
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
	// A deleted destination is still loaded: its type and config give the
	// repository address, and the caller supplies the wiped credentials.
	dest, err := h.dests.GetByIDIncludingDeleted(ctx, snapshot.DestinationID)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrBadRequest(w, "destination not found")
			return
		}
		h.logger.Error("failed to load destination for restore", zap.Error(err))
		ErrInternal(w)
		return
	}
	destDeleted := dest.DeletedAt.Valid
	if destDeleted && !applySuppliedCredentials(w, dest, &req) {
		return
	}

	// --- 3. Resolve the repo password (from the policy, or from the
	// destination when the snapshot was imported). The agent stays the one the
	// caller asked for: a restore may legitimately target another machine.
	repoPassword, _, err := h.resolveRepoAccess(ctx, snapshot, dest)
	if err != nil {
		missing := errors.Is(err, errPolicyMissing) || errors.Is(err, errRepoPasswordUnavailable)
		if !destDeleted || !missing {
			h.writeRepoAccessError(w, err, "restore")
			return
		}
		if req.RepoPassword == "" {
			ErrUnprocessable(w, "the repository password is required: the destination of this snapshot was deleted and no policy holds the password any more")
			return
		}
		repoPassword = req.RepoPassword
	}

	// --- 4. Require an online agent ---
	// A restore is never queued for later: it writes to a path the user chose
	// now, and starting it hours later on reconnect would be a surprise (the
	// pending queue only rebuilds backups — see Scheduler.DispatchPending).
	if !h.agentMgr.IsConnected(agentID.String()) {
		ErrServiceUnavailable(w, "agent is not connected — start the restore again once the agent is online")
		return
	}

	// --- 5. Create restore job ---
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

	// --- 6. Build and dispatch ---
	payload := restorePayload{
		ResticSnapshotID: snapshot.SnapshotID,
		RepoPassword:     repoPassword,
		TargetPath:       req.TargetPath,
		IncludePaths:     req.IncludePaths,
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
		h.failUndispatchedRestore(ctx, job.ID, "the restore could not be prepared")
		ErrInternal(w)
		return
	}

	assignment := &proto.JobAssignment{
		JobId:       job.ID.String(),
		PolicyId:    uuidString(job.PolicyID),
		Type:        proto.JobType_JOB_TYPE_RESTORE,
		Payload:     payloadBytes,
		ScheduledAt: timestamppb.Now(),
	}

	if err := h.agentMgr.Dispatch(agentID.String(), assignment); err != nil {
		if errors.Is(err, agentmanager.ErrAgentNotConnected) {
			// The agent dropped between the online check and the dispatch.
			h.failUndispatchedRestore(ctx, job.ID, "the agent went offline before the restore could start — start it again once the agent is online")
			ErrServiceUnavailable(w, "agent is not connected — start the restore again once the agent is online")
			return
		}
		h.logger.Error("failed to dispatch restore job",
			zap.String("job_id", job.ID.String()),
			zap.String("agent_id", agentID.String()),
			zap.Error(err),
		)
		h.failUndispatchedRestore(ctx, job.ID, "the restore could not be sent to the agent")
		ErrInternal(w)
		return
	}

	h.logger.Info("restore job dispatched",
		zap.String("job_id", job.ID.String()),
		zap.String("snapshot_id", snapshot.SnapshotID),
		zap.String("agent_id", agentID.String()),
		zap.String("target_path", req.TargetPath),
	)

	logAudit(r, h.auditRepo, h.logger, "snapshot.restore", "snapshot", snapshotID.String(), map[string]any{
		"snapshot_id":         snapshot.SnapshotID,
		"destination_id":      snapshot.DestinationID.String(),
		"destination_deleted": destDeleted,
		"target_path":         req.TargetPath,
		"agent_id":            agentID.String(),
	})
	Ok(w, restoreResponse{JobID: job.ID.String()})
}

// Browse handles GET /api/v1/snapshots/{id}/browse[?path=<dir>][&agent_id=<id>].
// Returns the direct children of one directory within the snapshot by
// dispatching a JOB_TYPE_LIST_SNAPSHOT_FILES request to the agent chosen by
// openSnapshot via the existing StreamJobs stream (same pattern as
// JOB_TYPE_LIST_VOLUMES), blocking until the result or a timeout.
//
// The listing is non-recursive: the GUI expands one directory level at a time
// (lazy loading), so each response stays small and fast even on snapshots with
// millions of files. An empty path lists the snapshot root.
func (h *SnapshotHandler) Browse(w http.ResponseWriter, r *http.Request) {
	snapshotID, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	access, agentID, ok := h.openSnapshot(w, r, snapshotID, r.URL.Query().Get("agent_id"), "browse")
	if !ok {
		return
	}
	browsePayload := snapshotBrowsePayload{snapshotRepoAccess: access, Path: r.URL.Query().Get("path")}

	payloadBytes, err := json.Marshal(browsePayload)
	if err != nil {
		h.logger.Error("failed to marshal browse payload", zap.Error(err))
		ErrInternal(w)
		return
	}

	// Running restic ls against a cold remote repository (the first expansion
	// loads the index) can exceed the server's default 30s write timeout. Extend
	// the write deadline past the agent wait (snapshotBrowseTimeout, 5m) so the
	// response — success or the 504 — can always be written.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(6 * time.Minute)); err != nil {
		h.logger.Debug("browse: could not extend write deadline", zap.Error(err))
	}

	correlationID := uuid.NewString()
	result, err := h.agentMgr.RequestSnapshotBrowse(r.Context(), agentID, correlationID, payloadBytes)
	if err != nil {
		switch {
		case errors.Is(err, agentmanager.ErrAgentNotConnected):
			ErrServiceUnavailable(w, "agent is not connected")
		case errors.Is(err, agentmanager.ErrSnapshotBrowseTimeout):
			errJSON(w, http.StatusGatewayTimeout, "snapshot browse timed out", "gateway_timeout")
		default:
			h.logger.Error("snapshot browse failed", zap.Error(err))
			ErrInternal(w)
		}
		return
	}
	if result.Err != "" {
		h.logger.Warn("agent reported error during snapshot browse", zap.String("error", result.Err))
		errJSON(w, http.StatusBadGateway, result.Err, "agent_error")
		return
	}

	// --- 5. Map proto entries to response ---
	entries := make([]snapshotFileEntryResponse, len(result.Entries))
	for i, e := range result.Entries {
		entries[i] = snapshotFileEntryResponse{
			Path:  e.Path,
			Type:  e.Type,
			Size:  e.Size,
			Mtime: e.Mtime,
		}
	}
	Ok(w, snapshotBrowseResponse{Entries: entries})
}

// -----------------------------------------------------------------------------
// Internal helpers
// -----------------------------------------------------------------------------

// openSnapshot loads the snapshot and its destination, resolves the repository
// password, and picks the agent that runs restic against it: agentID when the
// caller chose one — any agent that can reach the repository will do, e.g.
// when the machine that took the snapshot is gone — else the snapshot's policy
// agent. It writes the error response and reports ok=false on failure.
func (h *SnapshotHandler) openSnapshot(w http.ResponseWriter, r *http.Request, snapshotID uuid.UUID, agentID, action string) (snapshotRepoAccess, string, bool) {
	ctx := r.Context()

	snapshot, err := h.repo.GetByID(ctx, snapshotID)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return snapshotRepoAccess{}, "", false
		}
		h.logger.Error("failed to load snapshot", zap.String("action", action), zap.Error(err))
		ErrInternal(w)
		return snapshotRepoAccess{}, "", false
	}

	dest, err := h.dests.GetByIDIncludingDeleted(ctx, snapshot.DestinationID)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrBadRequest(w, "destination not found")
			return snapshotRepoAccess{}, "", false
		}
		h.logger.Error("failed to load destination", zap.String("action", action), zap.Error(err))
		ErrInternal(w)
		return snapshotRepoAccess{}, "", false
	}
	if dest.DeletedAt.Valid {
		ErrUnprocessable(w, "the destination of this snapshot was deleted — browsing and downloading are not available, but you can still restore the whole snapshot by entering its credentials")
		return snapshotRepoAccess{}, "", false
	}

	repoPassword, policyAgentID, err := h.resolveRepoAccess(ctx, snapshot, dest)
	if err != nil {
		h.writeRepoAccessError(w, err, action)
		return snapshotRepoAccess{}, "", false
	}
	if agentID == "" {
		// An imported snapshot has no policy to name an agent: the caller must.
		agentID = policyAgentID
	}
	if _, err := uuid.Parse(agentID); err != nil {
		ErrBadRequest(w, "agent_id is required for an imported snapshot and must be a valid UUID")
		return snapshotRepoAccess{}, "", false
	}
	if !h.agentMgr.IsConnected(agentID) {
		ErrServiceUnavailable(w, "agent is not connected — ensure the agent is online and try again")
		return snapshotRepoAccess{}, "", false
	}

	return snapshotRepoAccess{
		ResticSnapshotID: snapshot.SnapshotID,
		RepoPassword:     repoPassword,
		Destination: destinationFields{
			DestinationID: dest.ID.String(),
			Type:          dest.Type,
			RepoURL:       destutil.BuildRepoURL(dest),
			Env:           destutil.BuildEnv(dest),
		},
	}, agentID, true
}

func (h *SnapshotHandler) writeSnapshotList(w http.ResponseWriter, snapshots []repositories.SnapshotWithNames, total int64) {
	items := make([]snapshotResponse, len(snapshots))
	for i := range snapshots {
		items[i] = snapshotWithNamesToResponse(snapshots[i])
	}
	Ok(w, listSnapshotsResponse{Items: items, Total: total})
}
