package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/destutil"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// DestinationHandler groups all destination-related HTTP handlers.
type DestinationHandler struct {
	repo         repositories.DestinationRepository
	snapshotRepo repositories.SnapshotRepository
	policyRepo   repositories.PolicyRepository
	agentMgr     *agentmanager.Manager
	auditRepo    repositories.AuditRepository
	logger       *zap.Logger
}

// NewDestinationHandler creates a new DestinationHandler.
func NewDestinationHandler(
	repo repositories.DestinationRepository,
	snapshotRepo repositories.SnapshotRepository,
	policyRepo repositories.PolicyRepository,
	agentMgr *agentmanager.Manager,
	auditRepo repositories.AuditRepository,
	logger *zap.Logger,
) *DestinationHandler {
	return &DestinationHandler{
		repo:         repo,
		snapshotRepo: snapshotRepo,
		policyRepo:   policyRepo,
		agentMgr:     agentMgr,
		auditRepo:    auditRepo,
		logger:       logger.Named("destination_handler"),
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
		CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: d.UpdatedAt.UTC().Format(time.RFC3339),
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
// Supports an optional ?search= query parameter for substring name filtering.
func (h *DestinationHandler) List(w http.ResponseWriter, r *http.Request) {
	opts := paginationOpts(r)

	var destinations []db.Destination
	var total int64
	var err error

	if search := r.URL.Query().Get("search"); search != "" {
		destinations, total, err = h.repo.ListFiltered(r.Context(), repositories.DestinationFilter{Search: search}, opts)
	} else {
		destinations, total, err = h.repo.List(r.Context(), opts)
	}
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
	Credentials string `json:"credentials"`        // JSON, stored encrypted
	Config      string `json:"config"`              // JSON, not sensitive
	ImportAgentID      string `json:"import_agent_id,omitempty"`
	ImportRepoPassword string `json:"import_repo_password,omitempty"`
}

type createDestinationResponse struct {
	destinationResponse
	Import *importDestinationResponse `json:"import,omitempty"`
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

	ctx := r.Context()

	// If import fields are provided, test via agent BEFORE persisting anything.
	// BuildEnv/BuildRepoURL work on the in-memory dest because db.EncryptedString
	// returns the raw string when not persisted through GORM.
	var importResult *agentmanager.SnapshotImportResult
	if req.ImportAgentID != "" {
		env := destutil.BuildEnv(dest)
		env["RESTIC_PASSWORD"] = req.ImportRepoPassword
		payload := snapshotImportPayload{
			Type:    dest.Type,
			RepoURL: destutil.BuildRepoURL(dest),
			Env:     env,
		}
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			h.logger.Error("failed to marshal import payload", zap.Error(err))
			ErrInternal(w)
			return
		}
		// Listing snapshots on a cold remote repository can exceed the server's
		// default 30s write timeout; extend it so the response isn't severed.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Minute)); err != nil {
			h.logger.Debug("import: could not extend write deadline", zap.Error(err))
		}
		correlationID := uuid.New().String()
		res, err := h.agentMgr.RequestSnapshotImport(ctx, req.ImportAgentID, correlationID, payloadBytes)
		if err != nil {
			if errors.Is(err, agentmanager.ErrAgentNotConnected) {
				ErrConflict(w, "agent is not connected")
				return
			}
			if errors.Is(err, agentmanager.ErrSnapshotImportTimeout) {
				ErrServiceUnavailable(w, "agent did not respond in time")
				return
			}
			h.logger.Error("snapshot import test failed", zap.Error(err))
			ErrInternal(w)
			return
		}
		if res.Err != "" {
			ErrUnprocessable(w, extractResticMessage(res.Err))
			return
		}
		importResult = &res
	}

	if err := h.repo.Create(ctx, dest); err != nil {
		h.logger.Error("failed to create destination", zap.Error(err))
		ErrInternal(w)
		return
	}

	resp := createDestinationResponse{destinationResponse: destinationToResponse(dest)}

	if importResult != nil {
		imported := h.persistImportedSnapshots(ctx, dest, importResult)
		logAudit(r, h.auditRepo, h.logger, "destination.import", "destination", dest.ID.String(), map[string]any{
			"found":    len(importResult.Snapshots),
			"imported": imported,
		})
		resp.Import = &importDestinationResponse{Found: len(importResult.Snapshots), Imported: imported}
	}

	logAudit(r, h.auditRepo, h.logger, "destination.create", "destination", dest.ID.String(), map[string]any{"name": dest.Name, "type": dest.Type})
	Created(w, resp)
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
	if req.Credentials != nil && hasNonEmptyCredential(*req.Credentials) {
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

	logAudit(r, h.auditRepo, h.logger, "destination.update", "destination", id.String(), map[string]any{"name": dest.Name})
	Ok(w, destinationToResponse(dest))
}

// hasNonEmptyCredential reports whether the credentials JSON carries at least
// one non-empty value. Prevents an edit form that never echoes secrets back
// from wiping stored credentials with a blank payload (e.g. {"password":""}).
func hasNonEmptyCredential(creds string) bool {
	s := strings.TrimSpace(creds)
	if s == "" || s == "{}" {
		return false
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return true // not a flat string map — treat as a real update
	}
	for _, v := range m {
		if strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// importDestinationRequest is the body for POST /api/v1/destinations/{id}/import.
type importDestinationRequest struct {
	AgentID      string `json:"agent_id"`
	RepoPassword string `json:"repo_password"`
}

// importDestinationResponse reports how many snapshots were found and saved.
type importDestinationResponse struct {
	Found    int `json:"found"`
	Imported int `json:"imported"`
}

// snapshotImportPayload is the JSON payload sent to the agent for IMPORT_SNAPSHOTS.
type snapshotImportPayload struct {
	Type    string            `json:"type"`
	RepoURL string            `json:"repo_url"`
	Env     map[string]string `json:"env"`
}

// Import handles POST /api/v1/destinations/{id}/import.
// It dispatches a JOB_TYPE_IMPORT_SNAPSHOTS request to the chosen agent and
// persists any snapshots not already present in the database.
func (h *DestinationHandler) Import(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	var req importDestinationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.AgentID == "" {
		ErrBadRequest(w, "agent_id is required")
		return
	}
	if req.RepoPassword == "" {
		ErrBadRequest(w, "repo_password is required")
		return
	}

	agentID, err := uuid.Parse(req.AgentID)
	if err != nil {
		ErrBadRequest(w, "invalid agent_id: must be a valid UUID")
		return
	}
	_ = agentID // used as string below

	ctx := r.Context()

	dest, err := h.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get destination for import", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	env := destutil.BuildEnv(dest)
	env["RESTIC_PASSWORD"] = req.RepoPassword

	payload := snapshotImportPayload{
		Type:    dest.Type,
		RepoURL: destutil.BuildRepoURL(dest),
		Env:     env,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		h.logger.Error("failed to marshal import payload", zap.Error(err))
		ErrInternal(w)
		return
	}

	// Listing snapshots on a cold remote repository can exceed the server's
	// default 30s write timeout; extend it so the response isn't severed.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Minute)); err != nil {
		h.logger.Debug("import: could not extend write deadline", zap.Error(err))
	}
	correlationID := uuid.New().String()
	result, err := h.agentMgr.RequestSnapshotImport(ctx, req.AgentID, correlationID, payloadBytes)
	if err != nil {
		if errors.Is(err, agentmanager.ErrAgentNotConnected) {
			ErrConflict(w, "agent is not connected")
			return
		}
		if errors.Is(err, agentmanager.ErrSnapshotImportTimeout) {
			ErrServiceUnavailable(w, "agent did not respond in time")
			return
		}
		h.logger.Error("snapshot import request failed", zap.Error(err))
		ErrInternal(w)
		return
	}

	if result.Err != "" {
		ErrServiceUnavailable(w, extractResticMessage(result.Err))
		return
	}

	imported := h.persistImportedSnapshots(ctx, dest, &result)

	logAudit(r, h.auditRepo, h.logger, "destination.import", "destination", id.String(), map[string]any{
		"found":    len(result.Snapshots),
		"imported": imported,
	})
	Ok(w, importDestinationResponse{Found: len(result.Snapshots), Imported: imported})
}

// persistImportedSnapshots saves snapshots returned by a JOB_TYPE_IMPORT_SNAPSHOTS
// RPC call that have not yet been seen for this destination. Returns the count of
// newly persisted snapshots.
func (h *DestinationHandler) persistImportedSnapshots(ctx context.Context, dest *db.Destination, result *agentmanager.SnapshotImportResult) int {
	imported := 0
	for _, info := range result.Snapshots {
		exists, err := h.snapshotRepo.ExistsBySnapshotIDAndDestination(ctx, info.ResticSnapshotId, dest.ID)
		if err != nil {
			h.logger.Error("failed to check snapshot existence", zap.Error(err))
			continue
		}
		if exists {
			continue
		}

		snapshotAt, err := time.Parse(time.RFC3339Nano, info.SnapshotTime)
		if err != nil {
			snapshotAt, _ = time.Parse(time.RFC3339, info.SnapshotTime)
		}

		sourcesJSON, _ := json.Marshal(info.Paths)
		tagsJSON, _ := json.Marshal(info.Tags)

		snap := &db.Snapshot{
			PolicyID:      uuid.Nil,
			JobID:         uuid.Nil,
			DestinationID: dest.ID,
			IsImported:    true,
			SnapshotID:    info.ResticSnapshotId,
			Hostname:      info.Hostname,
			Sources:       string(sourcesJSON),
			Tags:          string(tagsJSON),
			SnapshotAt:    snapshotAt,
		}
		if err := h.snapshotRepo.Create(ctx, snap); err != nil {
			h.logger.Error("failed to create imported snapshot", zap.Error(err))
			continue
		}
		imported++
	}
	return imported
}

// Delete handles DELETE /api/v1/destinations/{id}.
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
		h.logger.Warn("failed to delete destination", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	if err := h.policyRepo.DeleteDestinationAssociations(r.Context(), id); err != nil {
		h.logger.Warn("failed to clean up policy associations after destination delete", zap.String("id", id.String()), zap.Error(err))
	}

	logAudit(r, h.auditRepo, h.logger, "destination.delete", "destination", id.String(), map[string]any{})
	NoContent(w)
}

// extractResticMessage parses a restic error string and returns only the
// human-readable message. Restic errors often look like:
//
//	restic: command failed: exit status 12 {"message_type":"exit_error","code":12,"message":"Fatal: ..."}
//
// If a JSON object with a "message" field is found, that value is returned.
// Otherwise the original string is returned unchanged.
func extractResticMessage(s string) string {
	if i := strings.Index(s, "{"); i >= 0 {
		var v struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(s[i:]), &v); err == nil && v.Message != "" {
			return v.Message
		}
	}
	return s
}