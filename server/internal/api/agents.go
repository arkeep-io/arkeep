package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// AgentHandler groups all agent-related HTTP handlers.
type AgentHandler struct {
	repo      repositories.AgentRepository
	manager   *agentmanager.Manager
	auditRepo repositories.AuditRepository
	logger    *zap.Logger
}

// NewAgentHandler creates a new AgentHandler.
func NewAgentHandler(repo repositories.AgentRepository, manager *agentmanager.Manager, auditRepo repositories.AuditRepository, logger *zap.Logger) *AgentHandler {
	return &AgentHandler{
		repo:      repo,
		manager:   manager,
		auditRepo: auditRepo,
		logger:    logger.Named("agent_handler"),
	}
}

// agentResponse is the JSON representation of an agent returned by the API.
type agentResponse struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Hostname        string  `json:"hostname"`
	IPAddress       string  `json:"ip_address"`
	OS              string  `json:"os"`
	Arch            string  `json:"arch"`
	Version         string  `json:"version"`
	Status          string  `json:"status"`
	DockerAvailable bool    `json:"docker_available"`
	IdentityBound   bool    `json:"identity_bound"` // bound to its mTLS client certificate (never without auto-PKI)
	LastSeenAt      *string `json:"last_seen_at"`
	CreatedAt       string  `json:"created_at"`
}

// agentToResponse converts a db.Agent to an agentResponse.
func agentToResponse(a *db.Agent) agentResponse {
	resp := agentResponse{
		ID:              a.ID.String(),
		Name:            a.Name,
		Hostname:        a.Hostname,
		IPAddress:       a.IPAddress,
		OS:              a.OS,
		Arch:            a.Arch,
		Version:         a.Version,
		Status:          a.Status,
		DockerAvailable: a.DockerAvailable,
		IdentityBound:   a.CertFingerprint != nil,
		CreatedAt:       a.CreatedAt.UTC().Format(time.RFC3339),
	}
	if a.LastSeenAt != nil {
		s := a.LastSeenAt.UTC().Format(time.RFC3339)
		resp.LastSeenAt = &s
	}
	return resp
}

// listAgentsResponse wraps a paginated list of agents.
type listAgentsResponse struct {
	Items []agentResponse `json:"items"`
	Total int64           `json:"total"`
}

// List handles GET /api/v1/agents.
// Returns a paginated list of agents. Soft-deleted agents are excluded.
// Supports an optional ?search= query parameter for substring name filtering,
// and ?sort= (name, hostname, os, version, status, last_seen) with ?order=desc.
func (h *AgentHandler) List(w http.ResponseWriter, r *http.Request) {
	opts := sortedPaginationOpts(r)

	var agents []db.Agent
	var total int64
	var err error

	search := r.URL.Query().Get("search")
	status := r.URL.Query().Get("status")
	if search != "" || status != "" {
		agents, total, err = h.repo.ListFiltered(r.Context(), repositories.AgentFilter{Search: search, Status: status}, opts)
	} else {
		agents, total, err = h.repo.List(r.Context(), opts)
	}
	if err != nil {
		h.logger.Error("failed to list agents", zap.Error(err))
		ErrInternal(w)
		return
	}

	items := make([]agentResponse, len(agents))
	for i := range agents {
		items[i] = agentToResponse(&agents[i])
	}

	Ok(w, listAgentsResponse{Items: items, Total: total})
}

// GetByID handles GET /api/v1/agents/{id}.
func (h *AgentHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	agent, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get agent", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	Ok(w, agentToResponse(agent))
}

// updateAgentRequest is the JSON body expected by PATCH /api/v1/agents/{id}.
// All fields are optional — only non-nil values are applied.
type updateAgentRequest struct {
	Name *string `json:"name"`
}

// Update handles PATCH /api/v1/agents/{id}.
func (h *AgentHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	var req updateAgentRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	agent, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get agent for update", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	if req.Name != nil {
		if *req.Name == "" {
			ErrBadRequest(w, "name cannot be empty")
			return
		}
		agent.Name = *req.Name
	}

	if err := h.repo.Update(r.Context(), agent); err != nil {
		h.logger.Error("failed to update agent", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	logAudit(r, h.auditRepo, h.logger, "agent.update", "agent", id.String(), map[string]any{"name": agent.Name})
	Ok(w, agentToResponse(agent))
}

// Delete handles DELETE /api/v1/agents/{id}.
// Soft-deletes the agent — the record is retained in the database.
func (h *AgentHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	if err := h.repo.Delete(r.Context(), id); err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to delete agent", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	logAudit(r, h.auditRepo, h.logger, "agent.delete", "agent", id.String(), map[string]any{})
	NoContent(w)
}

// ResetIdentity handles POST /api/v1/agents/{id}/reset-identity (admin only).
// It unbinds the agent from its client certificate, so the next certificate
// that registers as the agent binds it. Used when an agent re-enrolled and is
// refused because its new certificate does not match the bound one. A
// connected agent is unaffected: it rebinds its own certificate when it next
// registers.
func (h *AgentHandler) ResetIdentity(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	if err := h.repo.ResetCertFingerprint(r.Context(), id); err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to reset agent identity", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	logAudit(r, h.auditRepo, h.logger, "agent.reset_identity", "agent", id.String(), map[string]any{})
	NoContent(w)
}

// volumeInfoResponse is the JSON shape of a single Docker volume returned by
// GET /api/v1/agents/{id}/volumes.
type volumeInfoResponse struct {
	Name       string `json:"name"`
	Mountpoint string `json:"mountpoint"`
	Driver     string `json:"driver"`
}

// ListVolumes handles GET /api/v1/agents/{id}/volumes.
// It asks the connected agent to enumerate its Docker volumes and returns the
// results. Returns 409 if the agent is not connected, 503 if Docker is
// unavailable on the agent host, and 504 if the agent does not respond in time.
func (h *AgentHandler) ListVolumes(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	agentID := id.String()

	if !h.manager.IsConnected(agentID) {
		ErrConflict(w, "agent is not connected")
		return
	}

	correlationID := uuid.New().String()

	result, err := h.manager.RequestVolumeList(r.Context(), agentID, correlationID)
	if err != nil {
		switch err {
		case agentmanager.ErrAgentNotConnected:
			ErrConflict(w, "agent is not connected")
		case agentmanager.ErrVolumeListTimeout:
			errJSON(w, http.StatusGatewayTimeout, "agent did not respond in time", "timeout")
		default:
			h.logger.Error("volume list request failed",
				zap.String("agent_id", agentID),
				zap.Error(err),
			)
			ErrInternal(w)
		}
		return
	}

	// The agent may report a Docker-level error (e.g. daemon unavailable).
	if result.Err != "" {
		errJSON(w, http.StatusServiceUnavailable, result.Err, "docker_unavailable")
		return
	}

	volumes := make([]volumeInfoResponse, len(result.Volumes))
	for i, v := range result.Volumes {
		volumes[i] = volumeInfoResponse{
			Name:       v.Name,
			Mountpoint: v.Mountpoint,
			Driver:     v.Driver,
		}
	}

	Ok(w, volumes)
}

// -----------------------------------------------------------------------------
// Shared handler helpers
// -----------------------------------------------------------------------------

// parseUUID extracts and parses a UUID path parameter by name.
// Writes a 400 and returns false if the parameter is missing or malformed.
func parseUUID(w http.ResponseWriter, r *http.Request, param string) (uuid.UUID, bool) {
	raw := chi.URLParam(r, param)
	id, err := uuid.Parse(raw)
	if err != nil {
		ErrBadRequest(w, "invalid "+param+": must be a valid UUID")
		return uuid.UUID{}, false
	}
	return id, true
}

// paginationOpts reads limit and offset query parameters from the request.
// Defaults: limit=20, offset=0. Max limit is capped at 100.
func paginationOpts(r *http.Request) repositories.ListOptions {
	limit := 20
	offset := 0

	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 100 {
		limit = 100
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	return repositories.ListOptions{Limit: limit, Offset: offset}
}

// sortedPaginationOpts is paginationOpts plus the optional ?sort= and
// ?order=desc parameters of the lists that can be sorted. The sort key is
// validated against a whitelist in the repository layer; an unknown one falls
// back to the list's default order.
func sortedPaginationOpts(r *http.Request) repositories.ListOptions {
	opts := paginationOpts(r)
	opts.SortBy = r.URL.Query().Get("sort")
	opts.SortDesc = r.URL.Query().Get("order") == "desc"
	return opts
}