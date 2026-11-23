package api

import (
	"errors"
	"net/http"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/repairs"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/shared/repair"
)

// RepairHandler serves a destination's maintenance mode and repairs
// (ADR 0026). Every route is admin only.
type RepairHandler struct {
	dests     repositories.DestinationRepository
	svc       *repairs.Service
	queue     QueueNotifier // may be nil
	auditRepo repositories.AuditRepository
	logger    *zap.Logger
}

// NewRepairHandler creates a RepairHandler.
func NewRepairHandler(dests repositories.DestinationRepository, svc *repairs.Service, queue QueueNotifier, auditRepo repositories.AuditRepository, logger *zap.Logger) *RepairHandler {
	return &RepairHandler{dests: dests, svc: svc, queue: queue, auditRepo: auditRepo, logger: logger.Named("repair_handler")}
}

// maintenanceRequest is the body of PUT /api/v1/destinations/{id}/maintenance.
type maintenanceRequest struct {
	Enabled *bool `json:"enabled"`
}

// SetMaintenance handles PUT /api/v1/destinations/{id}/maintenance. Enabling
// it stops every job from claiming the destination: scheduled backups,
// sweeps and checks wait in the queue. Disabling it lets them start, in
// order; it is refused while a repair of the destination is in progress.
func (h *RepairHandler) SetMaintenance(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	var req maintenanceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		ErrBadRequest(w, "enabled is required")
		return
	}

	var err error
	action := "destination.maintenance_end"
	if *req.Enabled {
		action = "destination.maintenance_start"
		err = h.dests.StartMaintenance(r.Context(), id, requesterEmail(r))
	} else {
		err = h.dests.EndMaintenance(r.Context(), id)
	}
	switch {
	case errors.Is(err, repositories.ErrNotFound):
		ErrNotFound(w)
		return
	case errors.Is(err, repositories.ErrRepairInProgress):
		ErrConflict(w, "a repair of this destination is in progress: the maintenance ends with it")
		return
	case err != nil:
		h.logger.Error("failed to change maintenance mode", zap.String("destination_id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}
	if !*req.Enabled && h.queue != nil {
		// The jobs that waited for the maintenance can start now.
		h.queue.Notify()
	}

	logAudit(r, h.auditRepo, h.logger, action, "destination", id.String(), map[string]any{})
	Ok(w, map[string]bool{"enabled": *req.Enabled})
}

// repairRequest is the body of POST /api/v1/destinations/{id}/repair: a
// repair.Params, plus the destination's name typed by the admin to confirm a
// destructive action.
type repairRequest struct {
	repair.Params
	Confirm string `json:"confirm"`
}

// repairResponse is the answer to a repair request that was dispatched.
type repairResponse struct {
	JobID string `json:"job_id"`
	// MaintenanceStarted is true when the repair put the destination in
	// maintenance for its own duration.
	MaintenanceStarted bool `json:"maintenance_started"`
}

// Repair handles POST /api/v1/destinations/{id}/repair. It starts one
// restic repair command on the destination's maintenance agent and returns
// the job, whose page streams restic's output. Destructive actions
// (repair-snapshots --forget, repair-packs) need the destination's name in
// confirm.
func (h *RepairHandler) Repair(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	var req repairRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	p := req.Params
	if err := p.Validate(); err != nil {
		ErrBadRequest(w, err.Error())
		return
	}

	dest, err := h.dests.GetByID(r.Context(), id)
	if errors.Is(err, repositories.ErrNotFound) {
		ErrNotFound(w)
		return
	}
	if err != nil {
		h.logger.Error("failed to load destination for repair", zap.String("destination_id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}
	if p.Destructive() && req.Confirm != dest.Name {
		ErrBadRequest(w, "this repair changes the repository for good: type the destination's name in confirm")
		return
	}

	started, err := h.svc.Start(r.Context(), id, p, requesterEmail(r))
	switch {
	case errors.Is(err, repositories.ErrNotFound):
		ErrNotFound(w)
		return
	case errors.Is(err, repairs.ErrInvalidRequest):
		ErrBadRequest(w, err.Error())
		return
	case errors.Is(err, repositories.ErrDestinationBusy):
		ErrConflict(w, "the destination is busy: wait for its running jobs to end, or put it in maintenance and wait until the maintenance is effective")
		return
	case errors.Is(err, repositories.ErrRepairInProgress),
		errors.Is(err, repairs.ErrNoMaintenanceAgent),
		errors.Is(err, repairs.ErrRepairUnsupported),
		errors.Is(err, repairs.ErrAgentOffline),
		errors.Is(err, repairs.ErrPasswordUnknown):
		ErrConflict(w, err.Error())
		return
	case err != nil:
		h.logger.Error("failed to start repair", zap.String("destination_id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	logAudit(r, h.auditRepo, h.logger, "destination.repair", "destination", id.String(), map[string]any{
		"job_id":              started.Job.ID.String(),
		"repair":              p,
		"destructive":         p.Destructive(),
		"maintenance_started": started.Maintenance,
	})
	Ok(w, repairResponse{JobID: started.Job.ID.String(), MaintenanceStarted: started.Maintenance})
}

// requesterEmail is the email of the authenticated admin, recorded on the
// jobs and maintenance they start.
func requesterEmail(r *http.Request) string {
	if claims := claimsFromCtx(r.Context()); claims != nil {
		return claims.Email
	}
	return ""
}
