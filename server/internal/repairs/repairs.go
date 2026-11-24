// Package repairs starts repository repairs (ADR 0026): one restic repair
// command, run by a destination's maintenance agent while the destination is
// in maintenance.
//
// Unlike retention sweeps and integrity checks, a repair is never queued. It
// is refused when the destination is busy or another repair of it is in
// progress, because a destructive repair must not start later, unattended,
// once the situation has changed. On a destination that is not in
// maintenance, the repair puts it in maintenance for its own duration, so no
// backup, sweep or check can claim it meanwhile.
//
// Start flow:
//  1. Validate the request (shared/repair) and the destination's maintenance
//     agent: assigned, reporting the repair capability, connected
//  2. Resolve the repository password
//  3. Refuse early when the destination is busy or already being repaired,
//     before any job record is written
//  4. Create the Job (type "repair", its parameters and requester recorded)
//     and its JobDestination
//  5. Admit it (DestinationRepository.ClaimForRepair), which is authoritative
//     should another job have claimed the destination since step 3
//  6. Dispatch a JOB_TYPE_REPAIR JobAssignment to the agent
package repairs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/destutil"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	proto "github.com/arkeep-io/arkeep/shared/proto"
	"github.com/arkeep-io/arkeep/shared/repair"
)

// JobType is the type of a repair job record.
const JobType = "repair"

// Errors returned by Start. Every one of them means nothing was dispatched.
var (
	// ErrInvalidRequest wraps a validation error of the request.
	ErrInvalidRequest = errors.New("invalid repair request")
	// ErrNoMaintenanceAgent: the destination has no maintenance agent.
	ErrNoMaintenanceAgent = errors.New("the destination has no maintenance agent: assign one in its retention and check settings")
	// ErrRepairUnsupported: the maintenance agent did not report the repair
	// capability (an older agent, or a restic without the repair commands).
	ErrRepairUnsupported = errors.New("the destination's maintenance agent does not support repairs: upgrade the agent")
	// ErrAgentOffline: the maintenance agent is not connected.
	ErrAgentOffline = errors.New("the destination's maintenance agent is not connected")
	// ErrPasswordUnknown: no policy or import gave the repository password.
	ErrPasswordUnknown = errors.New("the repository password is unknown: attach a policy or import the repository first")
)

// Dispatcher sends a job to a connected agent (implemented by
// agentmanager.Manager).
type Dispatcher interface {
	IsConnected(agentID string) bool
	Dispatch(agentID string, job *proto.JobAssignment) error
}

// destinationPayload mirrors the struct of the same name in
// server/internal/checkscheduler and agent/internal/executor.
type destinationPayload struct {
	DestinationID string            `json:"destination_id"`
	Type          string            `json:"type"`
	RepoURL       string            `json:"repo_url"`
	Credentials   string            `json:"credentials"`
	Config        string            `json:"config"`
	Env           map[string]string `json:"env"`
}

// repairPayload is the JSON payload of a JOB_TYPE_REPAIR job — see
// agent/internal/executor's identical type, which this must match
// field-for-field.
type repairPayload struct {
	Destination  destinationPayload `json:"destination"`
	RepoPassword string             `json:"repo_password"`
	Repair       repair.Params      `json:"repair"`
}

// Service starts repairs. The zero value is not usable — create instances
// with New.
type Service struct {
	dests  repositories.DestinationRepository
	agents repositories.AgentRepository
	jobs   repositories.JobRepository
	agentM Dispatcher
	logger *zap.Logger
}

// New creates a repair Service.
func New(dests repositories.DestinationRepository, agents repositories.AgentRepository, jobs repositories.JobRepository, agentMgr Dispatcher, logger *zap.Logger) *Service {
	return &Service{dests: dests, agents: agents, jobs: jobs, agentM: agentMgr, logger: logger.Named("repairs")}
}

// Started describes a repair that was dispatched.
type Started struct {
	Job *db.Job
	// Maintenance is true when the repair put the destination in maintenance
	// for its own duration: it was not in maintenance before.
	Maintenance bool
}

// Start validates and dispatches repair p on the destination, on behalf of
// requestedBy (an admin's email). Errors: repositories.ErrNotFound,
// ErrInvalidRequest, ErrNoMaintenanceAgent, ErrRepairUnsupported,
// ErrAgentOffline, ErrPasswordUnknown, repositories.ErrDestinationBusy,
// repositories.ErrRepairInProgress, or a dispatch failure.
func (s *Service) Start(ctx context.Context, destinationID uuid.UUID, p repair.Params, requestedBy string) (*Started, error) {
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	dest, err := s.dests.GetByID(ctx, destinationID)
	if err != nil {
		return nil, err
	}
	if dest.RetentionAgentID == nil {
		return nil, ErrNoMaintenanceAgent
	}
	agentID := *dest.RetentionAgentID
	agent, err := s.agents.GetByID(ctx, agentID)
	if errors.Is(err, repositories.ErrNotFound) {
		return nil, ErrNoMaintenanceAgent
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load maintenance agent %s: %w", agentID, err)
	}
	if !agent.RepairAvailable {
		return nil, ErrRepairUnsupported
	}
	if !s.agentM.IsConnected(agentID.String()) {
		return nil, ErrAgentOffline
	}

	policies, err := s.dests.ListPoliciesByDestination(ctx, dest.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load policies of destination %s: %w", dest.ID, err)
	}
	password := destutil.RepoPassword(dest, policies)
	if password == "" {
		return nil, ErrPasswordUnknown
	}

	// Refuse the common cases before writing anything, so a refused request
	// leaves no failed job behind. ClaimForRepair below decides for good.
	locks, err := s.dests.ListLocks(ctx, []uuid.UUID{dest.ID})
	if err != nil {
		return nil, fmt.Errorf("failed to load the locks of destination %s: %w", dest.ID, err)
	}
	if len(locks) > 0 {
		return nil, repositories.ErrDestinationBusy
	}
	repairing, err := s.jobs.HasActiveJobOfType(ctx, JobType, dest.ID)
	if err != nil {
		return nil, err
	}
	if repairing {
		return nil, repositories.ErrRepairInProgress
	}

	params, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("failed to encode repair parameters: %w", err)
	}
	job := &db.Job{
		AgentID:     agentID,
		Type:        JobType,
		Status:      "pending",
		Params:      string(params),
		RequestedBy: requestedBy,
	}
	if err := s.jobs.Create(ctx, job); err != nil {
		return nil, fmt.Errorf("failed to create repair job record for destination %s: %w", dest.ID, err)
	}
	if err := s.jobs.CreateDestination(ctx, &db.JobDestination{JobID: job.ID, DestinationID: dest.ID, Status: "pending"}); err != nil {
		s.close(ctx, job.ID, "failed to record the repair's destination")
		return nil, fmt.Errorf("failed to create job destination record for repair job %s: %w", job.ID, err)
	}

	auto, err := s.dests.ClaimForRepair(ctx, dest.ID, job.ID, requestedBy)
	if err != nil {
		// Another job claimed the destination since the checks above.
		s.close(ctx, job.ID, err.Error())
		return nil, err
	}

	if err := s.dispatch(job, dest, password, p); err != nil {
		// The job is closed, which ends a maintenance it started.
		s.close(ctx, job.ID, "could not send the repair to the agent: "+err.Error())
		return nil, fmt.Errorf("failed to dispatch repair job: %w", err)
	}

	s.logger.Info("repair job dispatched",
		zap.String("job_id", job.ID.String()),
		zap.String("destination_id", dest.ID.String()),
		zap.String("agent_id", agentID.String()),
		zap.String("action", p.Action),
		zap.Bool("destructive", p.Destructive()),
		zap.Bool("maintenance_started", auto),
		zap.String("requested_by", requestedBy),
	)
	return &Started{Job: job, Maintenance: auto}, nil
}

// close marks a repair job that never reached its agent as failed.
func (s *Service) close(ctx context.Context, jobID uuid.UUID, msg string) {
	now := time.Now().UTC()
	if err := s.jobs.UpdateStatus(ctx, jobID, "failed", nil, &now, msg); err != nil {
		s.logger.Error("failed to close undispatched repair job", zap.String("job_id", jobID.String()), zap.Error(err))
	}
}

func (s *Service) dispatch(job *db.Job, dest *db.Destination, password string, p repair.Params) error {
	payload, err := json.Marshal(repairPayload{
		Destination: destinationPayload{
			DestinationID: dest.ID.String(),
			Type:          dest.Type,
			RepoURL:       destutil.BuildRepoURL(dest),
			Credentials:   string(dest.Credentials),
			Config:        dest.Config,
			Env:           destutil.BuildEnv(dest),
		},
		RepoPassword: password,
		Repair:       p,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal repair job payload: %w", err)
	}
	return s.agentM.Dispatch(job.AgentID.String(), &proto.JobAssignment{
		JobId:   job.ID.String(),
		Type:    proto.JobType_JOB_TYPE_REPAIR,
		Payload: payload,
	})
}
