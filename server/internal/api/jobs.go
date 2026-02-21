package api

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// JobHandler groups all job-related HTTP handlers.
// Jobs are read-only from the API perspective — they are created exclusively
// by the scheduler (scheduled or manual trigger) and updated by agents via gRPC.
type JobHandler struct {
	repo   repositories.JobRepository
	logger *zap.Logger
}

// NewJobHandler creates a new JobHandler.
func NewJobHandler(repo repositories.JobRepository, logger *zap.Logger) *JobHandler {
	return &JobHandler{
		repo:   repo,
		logger: logger.Named("job_handler"),
	}
}

// -----------------------------------------------------------------------------
// Response types
// -----------------------------------------------------------------------------

// jobDestinationResponse represents the result of a job on a single destination.
type jobDestinationResponse struct {
	ID            string  `json:"id"`
	DestinationID string  `json:"destination_id"`
	Status        string  `json:"status"`
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
	AgentID      string                   `json:"agent_id"`
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

// jobToResponse converts a db.Job and its associated slices to a jobResponse.
// destinations and logs are passed separately because they are no longer
// embedded in the Job struct (see db/models.go for rationale).
// Pass nil for both when building list responses where details are not needed.
func jobToResponse(j *db.Job, destinations []db.JobDestination, logs []db.JobLog) jobResponse {
	resp := jobResponse{
		ID:           j.ID.String(),
		PolicyID:     j.PolicyID.String(),
		AgentID:      j.AgentID.String(),
		Status:       j.Status,
		Error:        j.Error,
		Destinations: make([]jobDestinationResponse, len(destinations)),
		CreatedAt:    j.CreatedAt.UTC().String(),
	}

	if j.StartedAt != nil {
		s := j.StartedAt.UTC().String()
		resp.StartedAt = &s
	}
	if j.EndedAt != nil {
		s := j.EndedAt.UTC().String()
		resp.EndedAt = &s
	}

	for i, jd := range destinations {
		d := jobDestinationResponse{
			ID:            jd.ID.String(),
			DestinationID: jd.DestinationID.String(),
			Status:        jd.Status,
			SnapshotID:    jd.SnapshotID,
			SizeBytes:     jd.SizeBytes,
			Error:         jd.Error,
		}
		if jd.StartedAt != nil {
			s := jd.StartedAt.UTC().String()
			d.StartedAt = &s
		}
		if jd.EndedAt != nil {
			s := jd.EndedAt.UTC().String()
			d.EndedAt = &s
		}
		resp.Destinations[i] = d
	}

	// logs is unused in the job response body — they are served separately via
	// GET /jobs/{id}/logs. The parameter is accepted here to keep the function
	// signature consistent with GetByIDWithDetails, but ignored intentionally.
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
// Supports optional filtering by policy_id or agent_id via query parameters.
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

	jobs, total, err := h.repo.List(r.Context(), opts)
	if err != nil {
		h.logger.Error("failed to list jobs", zap.Error(err))
		ErrInternal(w)
		return
	}
	h.writeJobList(w, jobs, total)
}

// GetByID handles GET /api/v1/jobs/{id}.
// Returns the job with its destinations preloaded. Logs are served separately
// via GET /api/v1/jobs/{id}/logs to keep this response compact.
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
			Timestamp: l.Timestamp.UTC().String(),
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

// writeJobList converts a slice of db.Job to a listJobsResponse and writes it.
// Destinations are not included in list responses — only in single-job detail.
func (h *JobHandler) writeJobList(w http.ResponseWriter, jobs []db.Job, total int64) {
	items := make([]jobResponse, len(jobs))
	for i := range jobs {
		items[i] = jobToResponse(&jobs[i], nil, nil)
	}
	Ok(w, listJobsResponse{Items: items, Total: total})
}

// parseUUIDString parses a raw UUID string, returning an error if invalid.
// Used for query parameter parsing where parseUUID (path param) is not applicable.
func parseUUIDString(s string) (uuid.UUID, error) {
	return uuid.Parse(s)
}