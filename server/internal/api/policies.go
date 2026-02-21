package api

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/scheduler"
)

// PolicyHandler groups all policy-related HTTP handlers.
type PolicyHandler struct {
	repo      repositories.PolicyRepository
	scheduler *scheduler.Scheduler
	logger    *zap.Logger
}

// NewPolicyHandler creates a new PolicyHandler.
func NewPolicyHandler(repo repositories.PolicyRepository, sched *scheduler.Scheduler, logger *zap.Logger) *PolicyHandler {
	return &PolicyHandler{
		repo:      repo,
		scheduler: sched,
		logger:    logger.Named("policy_handler"),
	}
}

// -----------------------------------------------------------------------------
// Response types
// -----------------------------------------------------------------------------

// policyDestinationResponse represents a single destination entry in a policy.
type policyDestinationResponse struct {
	ID            string `json:"id"`
	DestinationID string `json:"destination_id"`
	Priority      int    `json:"priority"`
}

// policyResponse is the JSON representation of a policy.
// RepoPassword is intentionally omitted — it is write-only.
type policyResponse struct {
	ID               string                      `json:"id"`
	Name             string                      `json:"name"`
	AgentID          string                      `json:"agent_id"`
	Schedule         string                      `json:"schedule"`
	Enabled          bool                        `json:"enabled"`
	Sources          string                      `json:"sources"`
	RetentionDaily   int                         `json:"retention_daily"`
	RetentionWeekly  int                         `json:"retention_weekly"`
	RetentionMonthly int                         `json:"retention_monthly"`
	RetentionYearly  int                         `json:"retention_yearly"`
	HookPreBackup    string                      `json:"hook_pre_backup"`
	HookPostBackup   string                      `json:"hook_post_backup"`
	Destinations     []policyDestinationResponse `json:"destinations"`
	LastRunAt        *string                     `json:"last_run_at"`
	NextRunAt        *string                     `json:"next_run_at"`
	CreatedAt        string                      `json:"created_at"`
}

// policyToResponse converts a db.Policy and its associated PolicyDestination
// slice to a policyResponse. The destinations are passed separately because
// they are no longer embedded in the Policy struct (see db/models.go).
func policyToResponse(p *db.Policy, destinations []db.PolicyDestination) policyResponse {
	resp := policyResponse{
		ID:               p.ID.String(),
		Name:             p.Name,
		AgentID:          p.AgentID.String(),
		Schedule:         p.Schedule,
		Enabled:          p.Enabled,
		Sources:          p.Sources,
		RetentionDaily:   p.RetentionDaily,
		RetentionWeekly:  p.RetentionWeekly,
		RetentionMonthly: p.RetentionMonthly,
		RetentionYearly:  p.RetentionYearly,
		HookPreBackup:    p.HookPreBackup,
		HookPostBackup:   p.HookPostBackup,
		Destinations:     make([]policyDestinationResponse, len(destinations)),
		CreatedAt:        p.CreatedAt.UTC().String(),
	}

	for i, pd := range destinations {
		resp.Destinations[i] = policyDestinationResponse{
			ID:            pd.ID.String(),
			DestinationID: pd.DestinationID.String(),
			Priority:      pd.Priority,
		}
	}

	if p.LastRunAt != nil {
		s := p.LastRunAt.UTC().String()
		resp.LastRunAt = &s
	}
	if p.NextRunAt != nil {
		s := p.NextRunAt.UTC().String()
		resp.NextRunAt = &s
	}

	return resp
}

// listPoliciesResponse wraps a paginated list of policies.
type listPoliciesResponse struct {
	Items []policyResponse `json:"items"`
	Total int64            `json:"total"`
}

// -----------------------------------------------------------------------------
// Handlers
// -----------------------------------------------------------------------------

// List handles GET /api/v1/policies.
// Destinations are not preloaded in list view to keep the query lightweight.
// Use GET /api/v1/policies/{id} to retrieve a single policy with destinations.
func (h *PolicyHandler) List(w http.ResponseWriter, r *http.Request) {
	opts := paginationOpts(r)

	policies, total, err := h.repo.List(r.Context(), opts)
	if err != nil {
		h.logger.Error("failed to list policies", zap.Error(err))
		ErrInternal(w)
		return
	}

	items := make([]policyResponse, len(policies))
	for i := range policies {
		// Pass an empty slice — destinations are not fetched in list view.
		items[i] = policyToResponse(&policies[i], nil)
	}

	Ok(w, listPoliciesResponse{Items: items, Total: total})
}

// createPolicyRequest is the JSON body expected by POST /api/v1/policies.
type createPolicyRequest struct {
	Name             string                    `json:"name"`
	AgentID          string                    `json:"agent_id"`
	Schedule         string                    `json:"schedule"`
	Sources          string                    `json:"sources"` // JSON array
	RepoPassword     string                    `json:"repo_password"`
	RetentionDaily   int                       `json:"retention_daily"`
	RetentionWeekly  int                       `json:"retention_weekly"`
	RetentionMonthly int                       `json:"retention_monthly"`
	RetentionYearly  int                       `json:"retention_yearly"`
	HookPreBackup    string                    `json:"hook_pre_backup"`
	HookPostBackup   string                    `json:"hook_post_backup"`
	Destinations     []destinationEntryRequest `json:"destinations"`
}

// destinationEntryRequest represents a single destination entry in a create/update request.
type destinationEntryRequest struct {
	DestinationID string `json:"destination_id"`
	Priority      int    `json:"priority"`
}

// Create handles POST /api/v1/policies.
// Creates the policy, its destination associations, and registers it with
// the scheduler if enabled.
func (h *PolicyHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createPolicyRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := validateCreatePolicy(&req); err != nil {
		ErrBadRequest(w, err.Error())
		return
	}

	agentID, err := uuid.Parse(req.AgentID)
	if err != nil {
		ErrBadRequest(w, "agent_id must be a valid UUID")
		return
	}

	// Apply retention defaults for zero values.
	if req.RetentionDaily == 0 {
		req.RetentionDaily = 7
	}
	if req.RetentionWeekly == 0 {
		req.RetentionWeekly = 4
	}
	if req.RetentionMonthly == 0 {
		req.RetentionMonthly = 6
	}
	if req.RetentionYearly == 0 {
		req.RetentionYearly = 1
	}

	policy := &db.Policy{
		Name:             req.Name,
		AgentID:          agentID,
		Schedule:         req.Schedule,
		Enabled:          true,
		Sources:          req.Sources,
		RepoPassword:     db.EncryptedString(req.RepoPassword),
		RetentionDaily:   req.RetentionDaily,
		RetentionWeekly:  req.RetentionWeekly,
		RetentionMonthly: req.RetentionMonthly,
		RetentionYearly:  req.RetentionYearly,
		HookPreBackup:    req.HookPreBackup,
		HookPostBackup:   req.HookPostBackup,
	}

	if err := h.repo.Create(r.Context(), policy); err != nil {
		h.logger.Error("failed to create policy", zap.Error(err))
		ErrInternal(w)
		return
	}

	// Add destination associations.
	for _, d := range req.Destinations {
		destID, err := uuid.Parse(d.DestinationID)
		if err != nil {
			h.logger.Warn("skipping invalid destination_id in policy create",
				zap.String("destination_id", d.DestinationID),
			)
			continue
		}
		pd := &db.PolicyDestination{
			PolicyID:      policy.ID,
			DestinationID: destID,
			Priority:      d.Priority,
		}
		if err := h.repo.AddDestination(r.Context(), pd); err != nil {
			h.logger.Error("failed to add destination to policy",
				zap.String("policy_id", policy.ID.String()),
				zap.String("destination_id", d.DestinationID),
				zap.Error(err),
			)
		}
	}

	// Reload with destinations to return the full representation.
	full, destinations, err := h.repo.GetByIDWithDestinations(r.Context(), policy.ID)
	if err != nil {
		h.logger.Error("failed to reload policy after create", zap.Error(err))
		ErrInternal(w)
		return
	}

	// Register with scheduler if enabled.
	if policy.Enabled {
		if err := h.scheduler.AddPolicy(full); err != nil {
			// Non-fatal: the policy is persisted, scheduler can be resynced.
			h.logger.Error("failed to schedule policy after create",
				zap.String("policy_id", policy.ID.String()),
				zap.Error(err),
			)
		}
	}

	Created(w, policyToResponse(full, destinations))
}

// GetByID handles GET /api/v1/policies/{id}.
// Returns the policy with its destination associations.
func (h *PolicyHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	policy, destinations, err := h.repo.GetByIDWithDestinations(r.Context(), id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get policy", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	Ok(w, policyToResponse(policy, destinations))
}

// updatePolicyRequest is the JSON body for PATCH /api/v1/policies/{id}.
// All fields are optional — only non-nil values are applied.
type updatePolicyRequest struct {
	Name             *string `json:"name"`
	Schedule         *string `json:"schedule"`
	Enabled          *bool   `json:"enabled"`
	Sources          *string `json:"sources"`
	RepoPassword     *string `json:"repo_password"`
	RetentionDaily   *int    `json:"retention_daily"`
	RetentionWeekly  *int    `json:"retention_weekly"`
	RetentionMonthly *int    `json:"retention_monthly"`
	RetentionYearly  *int    `json:"retention_yearly"`
	HookPreBackup    *string `json:"hook_pre_backup"`
	HookPostBackup   *string `json:"hook_post_backup"`
}

// Update handles PATCH /api/v1/policies/{id}.
// After persisting changes, syncs the scheduler to reflect the new schedule
// or enabled state.
func (h *PolicyHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	var req updatePolicyRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	// Fetch current policy and its destinations.
	policy, destinations, err := h.repo.GetByIDWithDestinations(r.Context(), id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get policy for update", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	if req.Name != nil {
		if *req.Name == "" {
			ErrBadRequest(w, "name cannot be empty")
			return
		}
		policy.Name = *req.Name
	}
	if req.Schedule != nil {
		if *req.Schedule == "" {
			ErrBadRequest(w, "schedule cannot be empty")
			return
		}
		policy.Schedule = *req.Schedule
	}
	if req.Enabled != nil {
		policy.Enabled = *req.Enabled
	}
	if req.Sources != nil {
		policy.Sources = *req.Sources
	}
	if req.RepoPassword != nil {
		policy.RepoPassword = db.EncryptedString(*req.RepoPassword)
	}
	if req.RetentionDaily != nil {
		policy.RetentionDaily = *req.RetentionDaily
	}
	if req.RetentionWeekly != nil {
		policy.RetentionWeekly = *req.RetentionWeekly
	}
	if req.RetentionMonthly != nil {
		policy.RetentionMonthly = *req.RetentionMonthly
	}
	if req.RetentionYearly != nil {
		policy.RetentionYearly = *req.RetentionYearly
	}
	if req.HookPreBackup != nil {
		policy.HookPreBackup = *req.HookPreBackup
	}
	if req.HookPostBackup != nil {
		policy.HookPostBackup = *req.HookPostBackup
	}

	if err := h.repo.Update(r.Context(), policy); err != nil {
		h.logger.Error("failed to update policy", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	// Sync scheduler: handles enable/disable and schedule changes.
	if err := h.scheduler.UpdatePolicy(policy); err != nil {
		h.logger.Error("failed to sync scheduler after policy update",
			zap.String("policy_id", id.String()),
			zap.Error(err),
		)
	}

	Ok(w, policyToResponse(policy, destinations))
}

// Delete handles DELETE /api/v1/policies/{id}.
// Soft-deletes the policy and removes it from the scheduler.
func (h *PolicyHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	if err := h.repo.Delete(r.Context(), id); err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to delete policy", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	if err := h.scheduler.RemovePolicy(id); err != nil {
		h.logger.Warn("failed to remove policy from scheduler",
			zap.String("policy_id", id.String()),
			zap.Error(err),
		)
	}

	NoContent(w)
}

// Trigger handles POST /api/v1/policies/{id}/trigger.
// Manually triggers an immediate backup job for the policy, bypassing the
// cron schedule.
func (h *PolicyHandler) Trigger(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	if err := h.scheduler.TriggerNow(r.Context(), id); err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to trigger policy",
			zap.String("policy_id", id.String()),
			zap.Error(err),
		)
		ErrInternal(w)
		return
	}

	NoContent(w)
}

// -----------------------------------------------------------------------------
// Validation
// -----------------------------------------------------------------------------

// validateCreatePolicy checks required fields for policy creation.
func validateCreatePolicy(req *createPolicyRequest) error {
	if req.Name == "" {
		return errors.New("name is required")
	}
	if req.AgentID == "" {
		return errors.New("agent_id is required")
	}
	if req.Schedule == "" {
		return errors.New("schedule is required")
	}
	if req.Sources == "" {
		return errors.New("sources is required")
	}
	if req.RepoPassword == "" {
		return errors.New("repo_password is required")
	}
	return nil
}