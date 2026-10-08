// Package checkscheduler runs each Destination's repository integrity check
// (restic check) on its own independent schedule (issue #307). Restic only
// ever writes new data and never re-reads existing pack files, so damage to
// the repository (bit rot, interrupted uploads, deleted packs) would otherwise
// go unnoticed until a restore.
//
// It mirrors server/internal/retentionscheduler: one gocron entry per
// Destination, run by the destination's maintenance agent (RetentionAgentID),
// skipped outright when that agent is offline, and serialized against
// backups and retention sweeps by the destination's busy gate. Unlike
// retention, append-only destinations are checked too: restic check only
// reads the repository.
//
// Dispatch flow:
//  1. Tick fires (or TriggerNow)
//  2. If the maintenance agent is not connected, log and skip — nothing is
//     persisted, the next scheduled tick will try again
//  3. Create a Job (type "check", PolicyID nil) + one JobDestination
//  4. Acquire the destination's busy gate — if already held, the job waits in
//     status "waiting" and package destqueue starts it once the destination
//     is free
//  5. Dispatch a JOB_TYPE_VERIFY JobAssignment to the agent
//
// The outcome is recorded on the destination (LastCheck*) by the gRPC
// server when the agent reports the job's terminal status.
package checkscheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/destqueue"
	"github.com/arkeep-io/arkeep/server/internal/destutil"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// Check modes, stored in db.Destination.CheckMode.
const (
	ModeStructure = "structure" // repository structure and metadata only
	ModeSubset    = "subset"    // plus CheckSubsetPercent of the pack data
	ModeFull      = "full"      // plus all pack data
)

// destinationPayload mirrors the struct of the same name in
// server/internal/scheduler and agent/internal/executor — the resolved
// details of the one destination a check job verifies.
type destinationPayload struct {
	DestinationID string            `json:"destination_id"`
	Type          string            `json:"type"`
	RepoURL       string            `json:"repo_url"`
	Credentials   string            `json:"credentials"`
	Config        string            `json:"config"`
	Env           map[string]string `json:"env"`
}

// checkPayload is the JSON payload for a JOB_TYPE_VERIFY job — see
// agent/internal/executor's identical type, which this must match
// field-for-field.
type checkPayload struct {
	Destination   destinationPayload `json:"destination"`
	RepoPassword  string             `json:"repo_password"`
	Mode          string             `json:"mode"`
	SubsetPercent int                `json:"subset_percent"`
}

// CheckScheduler wraps gocron and coordinates check job creation and
// dispatch, one gocron entry per Destination. The zero value is not usable —
// create instances with New.
type CheckScheduler struct {
	cron     gocron.Scheduler
	dests    repositories.DestinationRepository
	jobs     repositories.JobRepository
	agentMgr *agentmanager.Manager
	logger   *zap.Logger
	running  atomic.Bool
	// queue holds checks whose destination is busy. Required before the
	// first check; set via SetQueue.
	queue *destqueue.Dispatcher
}

// SetQueue attaches the destination queue. Must be called before Start.
func (s *CheckScheduler) SetQueue(q *destqueue.Dispatcher) {
	s.queue = q
}

// New constructs a CheckScheduler. Call Start once the database connection is
// established.
func New(
	dests repositories.DestinationRepository,
	jobs repositories.JobRepository,
	agentMgr *agentmanager.Manager,
	logger *zap.Logger,
) (*CheckScheduler, error) {
	c, err := gocron.NewScheduler()
	if err != nil {
		return nil, fmt.Errorf("failed to create gocron scheduler: %w", err)
	}

	return &CheckScheduler{
		cron:     c,
		dests:    dests,
		jobs:     jobs,
		agentMgr: agentMgr,
		logger:   logger.Named("check_scheduler"),
	}, nil
}

// Start loads every destination with integrity checks enabled from the
// database, schedules them, and starts the underlying gocron scheduler.
func (s *CheckScheduler) Start(ctx context.Context) error {
	destinations, err := s.dests.ListWithCheckSchedule(ctx)
	if err != nil {
		return fmt.Errorf("failed to load destinations with check schedule: %w", err)
	}

	for i := range destinations {
		if err := s.addJob(&destinations[i]); err != nil {
			s.logger.Error("failed to schedule destination check",
				zap.String("destination_id", destinations[i].ID.String()),
				zap.String("destination_name", destinations[i].Name),
				zap.Error(err),
			)
		}
	}

	s.logger.Info("check scheduler started", zap.Int("destinations_scheduled", len(destinations)))
	s.cron.Start()
	s.running.Store(true)
	return nil
}

// Stop gracefully shuts down the underlying gocron scheduler, waiting for any
// currently running job functions to complete before returning.
func (s *CheckScheduler) Stop() error {
	if err := s.cron.Shutdown(); err != nil {
		return fmt.Errorf("check scheduler shutdown error: %w", err)
	}
	s.running.Store(false)
	s.logger.Info("check scheduler stopped")
	return nil
}

// IsRunning reports whether the scheduler has been started and not yet stopped.
func (s *CheckScheduler) IsRunning() bool {
	return s.running.Load()
}

// eligible reports whether a destination should have a gocron entry at all.
func eligible(dest *db.Destination) bool {
	return dest.CheckEnabled && dest.CheckSchedule != ""
}

func (s *CheckScheduler) addJob(dest *db.Destination) error {
	if !eligible(dest) {
		return nil
	}
	destID := dest.ID
	_, err := s.cron.NewJob(
		gocron.CronJob(dest.CheckSchedule, false),
		gocron.NewTask(func() {
			ctx := context.Background()
			d, err := s.dests.GetByID(ctx, destID)
			if err != nil {
				s.logger.Warn("failed to reload destination for check tick",
					zap.String("destination_id", destID.String()),
					zap.Error(err),
				)
				return
			}
			if !eligible(d) {
				return
			}
			if _, err := s.runJob(ctx, d); err != nil {
				s.logger.Warn("check tick did not dispatch",
					zap.String("destination_id", destID.String()),
					zap.Error(err),
				)
			}
		}),
		gocron.WithTags(destID.String()),
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	return err
}

// AddDestination schedules a destination's integrity check. Safe to call
// while the scheduler is running. Called by the destinations API handler
// after a destination is created.
func (s *CheckScheduler) AddDestination(dest *db.Destination) error {
	if err := s.addJob(dest); err != nil {
		return fmt.Errorf("failed to add destination %s to check scheduler: %w", dest.ID, err)
	}
	s.logger.Info("destination added to check scheduler",
		zap.String("destination_id", dest.ID.String()),
		zap.String("destination_name", dest.Name),
		zap.String("schedule", dest.CheckSchedule),
	)
	return nil
}

// RemoveDestination removes a destination from the check scheduler. Safe to
// call while the scheduler is running. Called on destination deletion.
func (s *CheckScheduler) RemoveDestination(destinationID uuid.UUID) error {
	s.cron.RemoveByTags(destinationID.String())
	s.logger.Info("destination removed from check scheduler", zap.String("destination_id", destinationID.String()))
	return nil
}

// UpdateDestination reschedules a destination after its check schedule or
// enabled state has changed.
func (s *CheckScheduler) UpdateDestination(dest *db.Destination) error {
	s.cron.RemoveByTags(dest.ID.String())

	if !eligible(dest) {
		s.logger.Info("destination check disabled, removed from scheduler",
			zap.String("destination_id", dest.ID.String()),
		)
		return nil
	}

	return s.AddDestination(dest)
}

// TriggerNow manually triggers an immediate integrity check of a destination,
// bypassing its cron schedule and its enabled flag. Returns the created Job,
// or an error if the check could not be dispatched (no maintenance agent, the
// agent is offline, or a check is already queued). A busy destination is not
// an error: the check is queued and its Job returned.
func (s *CheckScheduler) TriggerNow(ctx context.Context, destinationID uuid.UUID) (*db.Job, error) {
	dest, err := s.dests.GetByID(ctx, destinationID)
	if err != nil {
		return nil, fmt.Errorf("destination not found: %w", err)
	}
	s.logger.Info("manual check trigger requested",
		zap.String("destination_id", destinationID.String()),
		zap.String("destination_name", dest.Name),
	)
	return s.runJob(ctx, dest)
}

// runJob checks the repository password is known and the maintenance agent
// is connected, then creates the Job/JobDestination records and starts the
// check — or queues it when the destination is busy. Returns an error (with
// nothing persisted) if the password is unknown, the agent is offline or a
// check of this destination is already pending, queued or running.
func (s *CheckScheduler) runJob(ctx context.Context, dest *db.Destination) (*db.Job, error) {
	if dest.RetentionAgentID == nil {
		return nil, fmt.Errorf("destination %s has no maintenance agent assigned", dest.ID)
	}
	agentID := *dest.RetentionAgentID

	password, err := s.repoPassword(ctx, dest)
	if err != nil {
		return nil, err
	}

	if !s.agentMgr.IsConnected(agentID.String()) {
		return nil, fmt.Errorf("maintenance agent %s is not connected", agentID)
	}

	active, err := s.jobs.HasActiveJobOfType(ctx, "check", dest.ID)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, fmt.Errorf("a check of destination %s is already queued or running", dest.ID)
	}

	job := &db.Job{
		AgentID: agentID,
		Type:    "check",
		Status:  "pending",
	}
	if err := s.jobs.Create(ctx, job); err != nil {
		return nil, fmt.Errorf("failed to create check job record for destination %s: %w", dest.ID, err)
	}

	if err := s.jobs.CreateDestination(ctx, &db.JobDestination{
		JobID:         job.ID,
		DestinationID: dest.ID,
		Status:        "pending",
	}); err != nil {
		s.logger.Error("failed to create job destination record for check job",
			zap.String("job_id", job.ID.String()),
			zap.String("destination_id", dest.ID.String()),
			zap.Error(err),
		)
	}

	err = s.start(ctx, job, dest, password, s.queue.AdmitNew(job.ID))
	if errors.Is(err, destqueue.ErrNotAdmitted) {
		return job, s.queue.Enqueue(ctx, job.ID)
	}
	return job, err
}

// StartQueued implements destqueue.Starter for integrity checks. A check
// whose destination was deleted, or whose maintenance agent changed while it
// waited, is closed as failed: the next scheduled tick creates a fresh one.
func (s *CheckScheduler) StartQueued(ctx context.Context, job *db.Job, admit destqueue.AdmitFunc) error {
	destIDs, err := s.jobs.ListQueuedDestinationIDs(ctx, job.ID)
	if err != nil {
		return err
	}
	if len(destIDs) != 1 {
		return s.failQueued(ctx, job.ID, "the check has no destination left to run against")
	}
	dest, err := s.dests.GetByID(ctx, destIDs[0])
	if errors.Is(err, repositories.ErrNotFound) {
		return s.failQueued(ctx, job.ID, "the destination was deleted while this check was waiting")
	}
	if err != nil {
		return fmt.Errorf("failed to load destination %s: %w", destIDs[0], err)
	}
	if dest.RetentionAgentID == nil || *dest.RetentionAgentID != job.AgentID {
		return s.failQueued(ctx, job.ID, "the destination's maintenance agent changed while this check was waiting")
	}
	password, err := s.repoPassword(ctx, dest)
	if err != nil {
		return s.failQueued(ctx, job.ID, err.Error())
	}

	return s.start(ctx, job, dest, password, admit)
}

// repoPassword resolves the password of the destination's repository. A
// destination created without importing a repository only has it on its
// policies, so one with neither cannot be checked.
func (s *CheckScheduler) repoPassword(ctx context.Context, dest *db.Destination) (string, error) {
	policies, err := s.dests.ListPoliciesByDestination(ctx, dest.ID)
	if err != nil {
		return "", fmt.Errorf("failed to load policies of destination %s: %w", dest.ID, err)
	}
	password := destutil.RepoPassword(dest, policies)
	if password == "" {
		return "", fmt.Errorf("the repository password of destination %s is unknown: attach a policy or import the repository first", dest.ID)
	}
	return password, nil
}

func (s *CheckScheduler) failQueued(ctx context.Context, jobID uuid.UUID, errMsg string) error {
	now := time.Now().UTC()
	return s.jobs.UpdateStatus(ctx, jobID, "failed", nil, &now, errMsg)
}

// start claims the destination's busy gate through admit, then dispatches the
// check. When admit refuses, nothing is sent and destqueue.ErrNotAdmitted is
// returned.
func (s *CheckScheduler) start(ctx context.Context, job *db.Job, dest *db.Destination, password string, admit destqueue.AdmitFunc) error {
	admitted, err := admit(ctx, []uuid.UUID{dest.ID})
	if err != nil {
		return fmt.Errorf("failed to acquire destination busy gate: %w", err)
	}
	if !admitted {
		return destqueue.ErrNotAdmitted
	}

	if err := s.dispatch(job, dest, password); err != nil {
		if relErr := s.dests.ReleaseBusy(ctx, dest.ID, job.ID); relErr != nil {
			s.logger.Error("failed to release destination busy gate after dispatch failure",
				zap.String("destination_id", dest.ID.String()),
				zap.Error(relErr),
			)
		}
		return fmt.Errorf("failed to dispatch check job: %w", err)
	}

	return nil
}

func (s *CheckScheduler) dispatch(job *db.Job, dest *db.Destination, password string) error {
	payload := checkPayload{
		Destination: destinationPayload{
			DestinationID: dest.ID.String(),
			Type:          dest.Type,
			RepoURL:       destutil.BuildRepoURL(dest),
			Credentials:   string(dest.Credentials),
			Config:        dest.Config,
			Env:           destutil.BuildEnv(dest),
		},
		RepoPassword:  password,
		Mode:          dest.CheckMode,
		SubsetPercent: dest.CheckSubsetPercent,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal check job payload: %w", err)
	}

	assignment := &proto.JobAssignment{
		JobId:       job.ID.String(),
		Type:        proto.JobType_JOB_TYPE_VERIFY,
		Payload:     payloadBytes,
		ScheduledAt: timestamppb.Now(),
	}

	if err := s.agentMgr.Dispatch(job.AgentID.String(), assignment); err != nil {
		return fmt.Errorf("agentmanager dispatch error: %w", err)
	}

	s.logger.Info("check job dispatched",
		zap.String("job_id", job.ID.String()),
		zap.String("destination_id", dest.ID.String()),
		zap.String("agent_id", job.AgentID.String()),
		zap.String("mode", dest.CheckMode),
	)
	return nil
}
