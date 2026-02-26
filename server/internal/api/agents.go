package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// AgentHandler groups all agent-related HTTP handlers.
type AgentHandler struct {
	repo   repositories.AgentRepository
	logger *zap.Logger
}

// NewAgentHandler creates a new AgentHandler.
func NewAgentHandler(repo repositories.AgentRepository, logger *zap.Logger) *AgentHandler {
	return &AgentHandler{
		repo:   repo,
		logger: logger.Named("agent_handler"),
	}
}

// agentResponse is the JSON representation of an agent returned by the API.
type agentResponse struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Hostname   string  `json:"hostname"`
	IPAddress  string  `json:"ip_address"`
	OS         string  `json:"os"`
	Arch       string  `json:"arch"`
	Version    string  `json:"version"`
	Status     string  `json:"status"`
	Labels     string  `json:"labels"`
	LastSeenAt *string `json:"last_seen_at"`
	CreatedAt  string  `json:"created_at"`
}

// agentToResponse converts a db.Agent to an agentResponse.
func agentToResponse(a *db.Agent) agentResponse {
	resp := agentResponse{
		ID:        a.ID.String(),
		Name:      a.Name,
		Hostname:  a.Hostname,
		IPAddress: a.IPAddress,
		OS:        a.OS,
		Arch:      a.Arch,
		Version:   a.Version,
		Status:    a.Status,
		Labels:    a.Labels,
		CreatedAt: a.CreatedAt.UTC().String(),
	}
	if a.LastSeenAt != nil {
		s := a.LastSeenAt.UTC().String()
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
func (h *AgentHandler) List(w http.ResponseWriter, r *http.Request) {
	opts := paginationOpts(r)

	agents, total, err := h.repo.List(r.Context(), opts)
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

// createAgentRequest is the JSON body expected by POST /api/v1/agents.
//
// Agents use an auto-discovery model: when the agent binary starts, it
// connects via gRPC and the server upserts the record using the hostname
// reported by the agent itself. This REST endpoint exists so that admins
// can pre-create a named placeholder before the agent connects for the
// first time — useful for inventory purposes.
//
// Hostname and all system metadata are populated by the agent on first
// gRPC connection (Register RPC) and updated on every subsequent reconnect.
type createAgentRequest struct {
	Name string `json:"name"`
}

// Create handles POST /api/v1/agents.
// Pre-creates a named agent placeholder. The agent populates hostname,
// OS, arch, version and status automatically when it connects via gRPC.
func (h *AgentHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createAgentRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		ErrBadRequest(w, "name is required")
		return
	}

	agent := &db.Agent{
		Name:   req.Name,
		Status: "offline",
		Labels: "{}",
	}

	if err := h.repo.Create(r.Context(), agent); err != nil {
		h.logger.Error("failed to create agent", zap.Error(err))
		ErrInternal(w)
		return
	}

	Created(w, agentToResponse(agent))
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
	Name   *string `json:"name"`
	Labels *string `json:"labels"`
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
	if req.Labels != nil {
		agent.Labels = *req.Labels
	}

	if err := h.repo.Update(r.Context(), agent); err != nil {
		h.logger.Error("failed to update agent", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

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

	NoContent(w)
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