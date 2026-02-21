// Package scheduler manages the lifecycle of backup jobs triggered by policy
// schedules. It wraps gocron and integrates with PolicyRepository (to load and
// update policies), JobRepository (to persist job records), and AgentManager
// (to dispatch jobs to connected agents via the open gRPC stream).
//
// Each policy maps to exactly one gocron job, identified by the policy UUID.
// Jobs run in singleton mode: if a policy's previous job is still running when
// the next tick fires, the new execution is skipped to avoid overlapping backups.
//
// Dispatch flow:
//  1. Tick fires → create Job + JobDestination records in DB (status: pending)
//  2. Attempt immediate dispatch via AgentManager if agent is connected
//  3. If agent is offline, the job stays pending; AgentManager.Register will
//     call DispatchPending when the agent reconnects (wired up in the gRPC server)
package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// Scheduler wraps gocron and coordinates job creation and dispatch.
// The zero value is not usable — create instances with New.
type Scheduler struct {
	cron     gocron.Scheduler
	policies repositories.PolicyRepository
	jobs     repositories.JobRepository
	agentMgr *agentmanager.Manager
	logger   *zap.Logger
}

// New creates and configures a new Scheduler. Call Start to begin processing.
func New(
	policies repositories.PolicyRepository,
	jobs repositories.JobRepository,
	agentMgr *agentmanager.Manager,
	logger *zap.Logger,
) (*Scheduler, error) {
	s, err := gocron.NewScheduler()
	if err != nil {
		return nil, fmt.Errorf("failed to create gocron scheduler: %w", err)
	}

	return &Scheduler{
		cron:     s,
		policies: policies,
		jobs:     jobs,
		agentMgr: agentMgr,
		logger:   logger.Named("scheduler"),
	}, nil
}

// Start loads all enabled policies from the database, schedules them, and
// starts the underlying gocron scheduler. It should be called once at server
// startup, after the database connection is established.
func (s *Scheduler) Start(ctx context.Context) error {
	enabled, err := s.policies.ListEnabled(ctx)
	if err != nil {
		return fmt.Errorf("failed to load enabled policies: %w", err)
	}

	for i := range enabled {
		if err := s.addJob(&enabled[i]); err != nil {
			// Log and continue — a bad cron expression on one policy should
			// not prevent other policies from being scheduled.
			s.logger.Error("failed to schedule policy",
				zap.String("policy_id", enabled[i].ID.String()),
				zap.String("policy_name", enabled[i].Name),
				zap.Error(err),
			)
		}
	}

	s.logger.Info("scheduler started",
		zap.Int("policies_scheduled", len(enabled)),
	)

	s.cron.Start()
	return nil
}

// Stop gracefully shuts down the underlying gocron scheduler, waiting for any
// currently running job functions to complete before returning.
func (s *Scheduler) Stop() error {
	if err := s.cron.Shutdown(); err != nil {
		return fmt.Errorf("scheduler shutdown error: %w", err)
	}
	s.logger.Info("scheduler stopped")
	return nil
}

// AddPolicy schedules a newly created or re-enabled policy. Safe to call while
// the scheduler is running. Called by the REST handler after policy creation.
func (s *Scheduler) AddPolicy(policy *db.Policy) error {
	if err := s.addJob(policy); err != nil {
		return fmt.Errorf("failed to add policy %s to scheduler: %w", policy.ID, err)
	}
	s.logger.Info("policy added to scheduler",
		zap.String("policy_id", policy.ID.String()),
		zap.String("policy_name", policy.Name),
		zap.String("schedule", policy.Schedule),
	)
	return nil
}

// RemovePolicy removes a policy from the scheduler. Safe to call while the
// scheduler is running. Called by the REST handler after policy deletion or
// when a policy is disabled.
func (s *Scheduler) RemovePolicy(policyID uuid.UUID) error {
	s.cron.RemoveByTags(policyID.String())
	s.logger.Info("policy removed from scheduler",
		zap.String("policy_id", policyID.String()),
	)
	return nil
}

// UpdatePolicy reschedules a policy after its cron expression or enabled state
// has changed. It removes the existing gocron job and adds a new one. Called
// by the REST handler after a policy update.
func (s *Scheduler) UpdatePolicy(policy *db.Policy) error {
	// Remove the old job first; no-op if the policy was never scheduled
	// (e.g. it was created in disabled state).
	s.cron.RemoveByTags(policy.ID.String())

	if !policy.Enabled {
		// Policy was disabled — just remove, don't re-add.
		s.logger.Info("policy disabled, removed from scheduler",
			zap.String("policy_id", policy.ID.String()),
		)
		return nil
	}

	return s.AddPolicy(policy)
}

// TriggerNow manually triggers an immediate job run for a policy, bypassing
// the cron schedule. Used by the REST handler for on-demand backups.
// The job is created in the DB and dispatched to the agent immediately.
func (s *Scheduler) TriggerNow(ctx context.Context, policyID uuid.UUID) error {
	policy, destinations, err := s.policies.GetByIDWithDestinations(ctx, policyID)
	if err != nil {
		return fmt.Errorf("policy not found: %w", err)
	}
	s.logger.Info("manual trigger requested",
		zap.String("policy_id", policyID.String()),
		zap.String("policy_name", policy.Name),
	)
	return s.runJob(policy, destinations)
}

// DispatchPending looks up all pending jobs for a given agent and attempts to
// dispatch them via AgentManager. Called by the gRPC server when an agent
// reconnects, ensuring jobs created while the agent was offline are not lost.
func (s *Scheduler) DispatchPending(ctx context.Context, agentID uuid.UUID) {
	opts := repositories.ListOptions{Limit: 100, Offset: 0}
	pendingJobs, _, err := s.jobs.ListByAgent(ctx, agentID, opts)
	if err != nil {
		s.logger.Error("failed to fetch pending jobs for agent",
			zap.String("agent_id", agentID.String()),
			zap.Error(err),
		)
		return
	}

	for i := range pendingJobs {
		j := &pendingJobs[i]
		if j.Status != "pending" {
			continue
		}
		if err := s.dispatch(j); err != nil {
			s.logger.Warn("failed to dispatch pending job to reconnected agent",
				zap.String("job_id", j.ID.String()),
				zap.String("agent_id", agentID.String()),
				zap.Error(err),
			)
		}
	}
}

// -----------------------------------------------------------------------------
// Internal helpers
// -----------------------------------------------------------------------------

// addJob registers a single policy as a gocron job with singleton mode.
// The policy UUID is used as the gocron tag for later identification.
func (s *Scheduler) addJob(policy *db.Policy) error {
	_, err := s.cron.NewJob(
		gocron.CronJob(policy.Schedule, false),
		gocron.NewTask(func(p db.Policy) {
			// Re-fetch destinations at tick time to pick up any changes made
			// since the job was scheduled. The policy snapshot passed in via
			// closure may be stale if destinations were added or removed.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, destinations, err := s.policies.GetByIDWithDestinations(ctx, p.ID)
			if err != nil {
				s.logger.Error("failed to load destinations at tick time",
					zap.String("policy_id", p.ID.String()),
					zap.Error(err),
				)
				return
			}

			if err := s.runJob(&p, destinations); err != nil {
				s.logger.Error("job run failed",
					zap.String("policy_id", p.ID.String()),
					zap.String("policy_name", p.Name),
					zap.Error(err),
				)
			}
		}, *policy),
		gocron.WithTags(policy.ID.String()),
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	if err != nil {
		return fmt.Errorf("gocron.NewJob failed for policy %s (schedule: %q): %w",
			policy.ID, policy.Schedule, err)
	}
	return nil
}

// runJob is the core execution unit called by gocron on each tick (or manually
// via TriggerNow). It:
//  1. Creates the Job record in the DB
//  2. Creates a JobDestination record for each associated destination
//  3. Updates policy.LastRunAt and policy.NextRunAt
//  4. Attempts to dispatch the job to the agent via AgentManager
//
// destinations is the pre-fetched slice of PolicyDestination for this policy,
// passed in by the caller to avoid a redundant DB round-trip.
func (s *Scheduler) runJob(policy *db.Policy, destinations []db.PolicyDestination) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if !policy.Enabled {
		// Policy was disabled between schedule registration and tick — skip.
		s.logger.Info("skipping job for disabled policy",
			zap.String("policy_id", policy.ID.String()),
		)
		return nil
	}

	// --- Create Job record ---
	job := &db.Job{
		PolicyID: policy.ID,
		AgentID:  policy.AgentID,
		Status:   "pending",
	}
	if err := s.jobs.Create(ctx, job); err != nil {
		return fmt.Errorf("failed to create job record for policy %s: %w", policy.ID, err)
	}

	s.logger.Info("job created",
		zap.String("job_id", job.ID.String()),
		zap.String("policy_id", policy.ID.String()),
		zap.String("policy_name", policy.Name),
		zap.String("agent_id", policy.AgentID.String()),
	)

	// --- Create JobDestination records ---
	for _, pd := range destinations {
		jd := &db.JobDestination{
			JobID:         job.ID,
			DestinationID: pd.DestinationID,
			Status:        "pending",
		}
		if err := s.jobs.CreateDestination(ctx, jd); err != nil {
			// Log but continue — we still want to attempt other destinations.
			s.logger.Error("failed to create job destination record",
				zap.String("job_id", job.ID.String()),
				zap.String("destination_id", pd.DestinationID.String()),
				zap.Error(err),
			)
		}
	}

	// --- Update policy schedule timestamps ---
	now := time.Now().UTC()
	// NextRunAt is not computed here because gocron manages its own schedule.
	// We only update LastRunAt to reflect when the job was last triggered.
	if err := s.policies.UpdateSchedule(ctx, policy.ID, now, now); err != nil {
		// Non-fatal — the job was already created, just log the failure.
		s.logger.Warn("failed to update policy schedule timestamps",
			zap.String("policy_id", policy.ID.String()),
			zap.Error(err),
		)
	}

	// --- Dispatch to agent ---
	if err := s.dispatch(job); err != nil {
		// Non-fatal: the job is persisted as pending. DispatchPending will
		// retry when the agent reconnects.
		s.logger.Warn("dispatch failed, job remains pending",
			zap.String("job_id", job.ID.String()),
			zap.String("agent_id", policy.AgentID.String()),
			zap.Error(err),
		)
	}

	return nil
}

// dispatch sends a JobAssignment proto message to the agent via AgentManager.
// Returns an error if the agent is not connected or the send fails.
func (s *Scheduler) dispatch(job *db.Job) error {
	assignment := &proto.JobAssignment{
		JobId:    job.ID.String(),
		PolicyId: job.PolicyID.String(),
	}

	if err := s.agentMgr.Dispatch(job.AgentID.String(), assignment); err != nil {
		return fmt.Errorf("agentmanager dispatch error: %w", err)
	}

	s.logger.Info("job dispatched",
		zap.String("job_id", job.ID.String()),
		zap.String("agent_id", job.AgentID.String()),
	)
	return nil
}