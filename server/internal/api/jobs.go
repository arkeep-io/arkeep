package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
)

// JobHandler groups all job-related HTTP handlers.
type JobHandler struct {
	repo   repositories.JobRepository
	agents *agentmanager.Manager
	hub    *websocket.Hub
	logger *zap.Logger
}

// NewJobHandler creates a new JobHandler.
func NewJobHandler(repo repositories.JobRepository, agents *agentmanager.Manager, hub *websocket.Hub, logger *zap.Logger) *JobHandler {
	return &JobHandler{
		repo:   repo,
		agents: agents,
		hub:    hub,
		logger: logger.Named("job_handler"),
	}
}

// -----------------------------------------------------------------------------
// Response types
// -----------------------------------------------------------------------------

// jobDestinationResponse represents the result of a job on a single destination.
type jobDestinationResponse struct {
	ID              string  `json:"id"`
	DestinationID   string  `json:"destination_id"`
	DestinationName string  `json:"destination_name"`
	Status          string  `json:"status"`
	SnapshotID    string  `json:"snapshot_id"`
	SizeBytes     int64   `json:"size_bytes"`
	StartedAt     *string `json:"started_at"`
	EndedAt       *string `json:"ended_at"`
	Error         string  `json:"error"`
}

// jobResponse is the JSON representation of a job.
type jobResponse struct {
	ID           string                   `json:"id"`
	PolicyID     string                   `json:"policy_id"`
	PolicyName   string                   `json:"policy_name"`
	AgentID      string                   `json:"agent_id"`
	AgentName    string                   `json:"agent_name"`
	Type         string                   `json:"type"`
	Status       string                   `json:"status"`
	Error        string                   `json:"error"`
	StartedAt    *string                  `json:"started_at"`
	EndedAt      *string                  `json:"ended_at"`
	Destinations []jobDestinationResponse `json:"destinations,omitempty"`
	CreatedAt    string                   `json:"created_at"`
}

// jobLogResponse represents a single log line from a job execution.
type jobLogResponse struct {
	ID        string `json:"id"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// jobToResponse converts a JobWithNames and its associated slices to a
// jobResponse. destinations and logs are passed separately because they are
// not embedded in the Job struct (see db/models.go for rationale).
// Pass nil for both when building list responses where details are not needed.
func jobToResponse(j *repositories.JobWithNames, destinations []repositories.JobDestinationWithName, logs []db.JobLog) jobResponse {
	resp := jobResponse{
		ID:           j.ID.String(),
		PolicyID:     uuidString(j.PolicyID),
		PolicyName:   j.PolicyName,
		AgentID:      j.AgentID.String(),
		AgentName:    j.AgentName,
		Type:         j.Type,
		Status:       j.Status,
		Error:        j.Error,
		Destinations: make([]jobDestinationResponse, len(destinations)),
		CreatedAt:    j.CreatedAt.UTC().Format(time.RFC3339),
	}

	if j.StartedAt != nil {
		s := j.StartedAt.UTC().Format(time.RFC3339)
		resp.StartedAt = &s
	}
	if j.EndedAt != nil {
		s := j.EndedAt.UTC().Format(time.RFC3339)
		resp.EndedAt = &s
	}

	for i, jd := range destinations {
		d := jobDestinationResponse{
			ID:              jd.ID.String(),
			DestinationID:   jd.DestinationID.String(),
			DestinationName: jd.DestinationName,
			Status:          jd.Status,
			SnapshotID:      jd.SnapshotID,
			SizeBytes:       jd.SizeBytes,
			Error:           jd.Error,
		}
		if jd.StartedAt != nil {
			s := jd.StartedAt.UTC().Format(time.RFC3339)
			d.StartedAt = &s
		}
		if jd.EndedAt != nil {
			s := jd.EndedAt.UTC().Format(time.RFC3339)
			d.EndedAt = &s
		}
		resp.Destinations[i] = d
	}

	// logs is unused in the job response body — served separately via
	// GET /jobs/{id}/logs. Accepted here to keep the call sites uniform.
	_ = logs

	return resp
}

// listJobsResponse wraps a paginated list of jobs.
type listJobsResponse struct {
	Items []jobResponse `json:"items"`
	Total int64         `json:"total"`
}

// -----------------------------------------------------------------------------
// Handlers
// -----------------------------------------------------------------------------

// List handles GET /api/v1/jobs.
// Supports optional filtering by policy_id, agent_id, status, and type via query parameters.
// Destinations are not included in list responses — use GET /jobs/{id} for details.
func (h *JobHandler) List(w http.ResponseWriter, r *http.Request) {
	opts := paginationOpts(r)

	// Optional filters — if both are provided, policy_id takes precedence.
	if policyID := r.URL.Query().Get("policy_id"); policyID != "" {
		id, err := parseUUIDString(policyID)
		if err != nil {
			ErrBadRequest(w, "invalid policy_id: must be a valid UUID")
			return
		}
		jobs, total, err := h.repo.ListByPolicy(r.Context(), id, opts)
		if err != nil {
			h.logger.Error("failed to list jobs by policy", zap.Error(err))
			ErrInternal(w)
			return
		}
		h.writeJobList(w, jobs, total)
		return
	}

	if agentID := r.URL.Query().Get("agent_id"); agentID != "" {
		id, err := parseUUIDString(agentID)
		if err != nil {
			ErrBadRequest(w, "invalid agent_id: must be a valid UUID")
			return
		}
		jobs, total, err := h.repo.ListByAgent(r.Context(), id, opts)
		if err != nil {
			h.logger.Error("failed to list jobs by agent", zap.Error(err))
			ErrInternal(w)
			return
		}
		h.writeJobList(w, jobs, total)
		return
	}

	var filter repositories.JobFilter

	if status := r.URL.Query().Get("status"); status != "" {
		switch status {
		case "pending", "running", "succeeded", "failed", "cancelled", "interrupted":
			filter.Status = status
		default:
			ErrBadRequest(w, "invalid status: must be one of pending, running, succeeded, failed, cancelled, interrupted")
			return
		}
	}

	if jobType := r.URL.Query().Get("type"); jobType != "" {
		switch jobType {
		case "backup", "restore":
			filter.Type = jobType
		default:
			ErrBadRequest(w, "invalid type: must be one of backup, restore")
			return
		}
	}

	if filter.Status != "" || filter.Type != "" {
		jobs, total, err := h.repo.ListFiltered(r.Context(), filter, opts)
		if err != nil {
			h.logger.Error("failed to list filtered jobs", zap.Error(err))
			ErrInternal(w)
			return
		}
		h.writeJobList(w, jobs, total)
		return
	}

	jobs, total, err := h.repo.List(r.Context(), opts)
	if err != nil {
		h.logger.Error("failed to list jobs", zap.Error(err))
		ErrInternal(w)
		return
	}
	h.writeJobList(w, jobs, total)
}

// GetByID handles GET /api/v1/jobs/{id}.
// Returns the job (with policy and agent names) and its destinations.
// Logs are served separately via GET /api/v1/jobs/{id}/logs to keep this
// response compact.
func (h *JobHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	job, destinations, logs, err := h.repo.GetByIDWithDetails(r.Context(), id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get job", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	Ok(w, jobToResponse(job, destinations, logs))
}

// GetLogs handles GET /api/v1/jobs/{id}/logs.
// Returns all log lines for the job ordered by timestamp ascending.
func (h *JobHandler) GetLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	logs, err := h.repo.GetLogs(r.Context(), id)
	if err != nil {
		h.logger.Error("failed to get job logs", zap.String("job_id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	items := make([]jobLogResponse, len(logs))
	for i, l := range logs {
		items[i] = jobLogResponse{
			ID:        l.ID.String(),
			Level:     l.Level,
			Message:   l.Message,
			Timestamp: l.Timestamp.UTC().Format(time.RFC3339),
		}
	}

	Ok(w, items)
}

// ListByPolicy handles GET /api/v1/policies/{id}/jobs.
// Returns a paginated list of jobs for a specific policy.
func (h *JobHandler) ListByPolicy(w http.ResponseWriter, r *http.Request) {
	policyID, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	opts := paginationOpts(r)

	jobs, total, err := h.repo.ListByPolicy(r.Context(), policyID, opts)
	if err != nil {
		h.logger.Error("failed to list jobs by policy",
			zap.String("policy_id", policyID.String()),
			zap.Error(err),
		)
		ErrInternal(w)
		return
	}

	h.writeJobList(w, jobs, total)
}

// -----------------------------------------------------------------------------
// Internal helpers
// -----------------------------------------------------------------------------

// writeJobList converts a slice of JobWithNames to a listJobsResponse and writes it.
// Destinations are not included in list responses — only in single-job detail.
func (h *JobHandler) writeJobList(w http.ResponseWriter, jobs []repositories.JobWithNames, total int64) {
	items := make([]jobResponse, len(jobs))
	for i := range jobs {
		items[i] = jobToResponse(&jobs[i], nil, nil)
	}
	Ok(w, listJobsResponse{Items: items, Total: total})
}

// Cancel handles POST /api/v1/jobs/{id}/cancel.
// Marks the job as cancelled in the database and, if the job is running,
// sends a cancel signal to the agent. Pending jobs are cancelled immediately
// without agent involvement.
func (h *JobHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	job, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get job for cancellation", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	if job.Status != "pending" && job.Status != "running" {
		ErrConflict(w, "job is already in a terminal state")
		return
	}

	now := time.Now().UTC()
	if err := h.repo.UpdateStatus(r.Context(), id, "cancelled", job.StartedAt, &now, ""); err != nil {
		// The job reached a terminal state between the read above and this write.
		if errors.Is(err, repositories.ErrTerminalState) {
			ErrConflict(w, "job is already in a terminal state")
			return
		}
		h.logger.Error("failed to cancel job", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	// For running jobs, notify the agent to abort. The agent reports
	// JOB_STATUS_CANCELLED, which the already-cancelled DB row now refuses.
	if job.Status == "running" {
		if err := h.agents.SendCancel(job.AgentID.String(), id.String()); err != nil {
			// Non-fatal: the DB is already updated; the agent will detect shutdown
			// or orphan recovery will handle it.
			h.logger.Warn("could not send cancel signal to agent",
				zap.String("job_id", id.String()),
				zap.String("agent_id", job.AgentID.String()),
				zap.Error(err),
			)
		}
	}

	// Publish a WebSocket event so all open job-detail pages update immediately.
	h.hub.Publish("job:"+id.String(), websocket.Message{
		Type: websocket.MsgJobStatus,
		Payload: map[string]any{
			"job_id":      id.String(),
			"status":      "cancelled",
			"finished_at": now.Format(time.RFC3339),
		},
	})

	Ok(w, map[string]string{"status": "cancelled"})
}

// parseUUIDString parses a raw UUID string, returning an error if invalid.
// Used for query parameter parsing where parseUUID (path param) is not applicable.
func parseUUIDString(s string) (uuid.UUID, error) {
	return uuid.Parse(s)
}