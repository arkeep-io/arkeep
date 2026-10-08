// Package retentionscheduler runs each Destination's retention sweep
// (restic forget --prune) on its own independent schedule, fully detached
// from backup jobs (issue #130). It mirrors server/internal/scheduler's
// gocron-based, one-job-per-entity structure, but for Destinations instead
// of Policies, and with one deliberate divergence: an offline retention
// agent causes the tick to be skipped outright (no pending Job row, no
// DispatchPending-style retry queue) rather than queued for later delivery.
//
// Dispatch flow:
//  1. Tick fires (or TriggerNow) → resolve the destination's live policies,
//     one restic tag per policy plus one per that policy's command sources
//  2. If the retention agent is not connected, log and skip — nothing is
//     persisted, the next scheduled tick will try again
//  3. Create a Job (type "retention", PolicyID nil) + one JobDestination
//  4. Acquire the destination's busy gate (server/internal/repositories'
//     Destination.BusyJobID) — if already held by another operation
//     (backup or retention), the job waits in status "waiting" and the
//     destination queue (package destqueue) starts it once the destination
//     is free (issue #285)
//  5. Create one JobRetentionTag per tag, then dispatch a JOB_TYPE_FORGET
//     JobAssignment to the agent
package retentionscheduler

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
	"github.com/arkeep-io/arkeep/server/internal/policyutil"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// destinationPayload mirrors the struct of the same name in
// server/internal/scheduler and agent/internal/executor — the resolved
// details of the one destination a retention job sweeps.
type destinationPayload struct {
	DestinationID string            `json:"destination_id"`
	Type          string            `json:"type"`
	RepoURL       string            `json:"repo_url"`
	Credentials   string            `json:"credentials"`
	Config        string            `json:"config"`
	Env           map[string]string `json:"env"`
}

// retentionPayload mirrors the keep_* fields, now sourced from db.Destination
// instead of db.Policy.
type retentionPayload struct {
	Last    int `json:"last"`
	Hourly  int `json:"hourly"`
	Daily   int `json:"daily"`
	Weekly  int `json:"weekly"`
	Monthly int `json:"monthly"`
	Yearly  int `json:"yearly"`
}

// retentionSweepPayload is the JSON payload for a standalone JOB_TYPE_FORGET
// job — see agent/internal/executor's identical type, which this must match
// field-for-field.
type retentionSweepPayload struct {
	Destination  destinationPayload `json:"destination"`
	RepoPassword string             `json:"repo_password"`
	Retention    retentionPayload   `json:"retention"`
	Tags         []string           `json:"tags"`
}

// RetentionScheduler wraps gocron and coordinates retention job creation and
// dispatch, one gocron entry per Destination. The zero value is not usable —
// create instances with New.
type RetentionScheduler struct {
	cron     gocron.Scheduler
	dests    repositories.DestinationRepository
	jobs     repositories.JobRepository
	agentMgr *agentmanager.Manager
	logger   *zap.Logger
	running  atomic.Bool
	// queue holds sweeps whose destination is busy (issue #285). Required
	// before the first sweep; set via SetQueue.
	queue *destqueue.Dispatcher
}

// SetQueue attaches the destination queue. Must be called before Start.
func (s *RetentionScheduler) SetQueue(q *destqueue.Dispatcher) {
	s.queue = q
}

// New constructs a RetentionScheduler. Call Start once the database
// connection is established.
func New(
	dests repositories.DestinationRepository,
	jobs repositories.JobRepository,
	agentMgr *agentmanager.Manager,
	logger *zap.Logger,
) (*RetentionScheduler, error) {
	c, err := gocron.NewScheduler()
	if err != nil {
		return nil, fmt.Errorf("failed to create gocron scheduler: %w", err)
	}

	return &RetentionScheduler{
		cron:     c,
		dests:    dests,
		jobs:     jobs,
		agentMgr: agentMgr,
		logger:   logger.Named("retention_scheduler"),
	}, nil
}

// Start loads every destination with retention enabled (and not append-only)
// from the database, schedules them, and starts the underlying gocron
// scheduler. Call once at server startup, after the database connection is
// established.
func (s *RetentionScheduler) Start(ctx context.Context) error {
	destinations, err := s.dests.ListWithRetentionSchedule(ctx)
	if err != nil {
		return fmt.Errorf("failed to load destinations with retention schedule: %w", err)
	}

	for i := range destinations {
		if err := s.addJob(&destinations[i]); err != nil {
			s.logger.Error("failed to schedule destination retention",
				zap.String("destination_id", destinations[i].ID.String()),
				zap.String("destination_name", destinations[i].Name),
				zap.Error(err),
			)
		}
	}

	s.logger.Info("retention scheduler started", zap.Int("destinations_scheduled", len(destinations)))
	s.cron.Start()
	s.running.Store(true)
	return nil
}

// Stop gracefully shuts down the underlying gocron scheduler, waiting for any
// currently running job functions to complete before returning. A forget
// --prune sweep can take minutes, so this stronger guarantee (mirroring the
// backup Scheduler's Stop, not the bare-goroutine style of simpler periodic
// services) matters for a clean shutdown.
func (s *RetentionScheduler) Stop() error {
	if err := s.cron.Shutdown(); err != nil {
		return fmt.Errorf("retention scheduler shutdown error: %w", err)
	}
	s.running.Store(false)
	s.logger.Info("retention scheduler stopped")
	return nil
}

// IsRunning reports whether the scheduler has been started and not yet stopped.
func (s *RetentionScheduler) IsRunning() bool {
	return s.running.Load()
}

// eligible reports whether a destination should have a gocron entry at all —
// append-only repositories can never support forget --prune, so the agent is
// never even asked to try (issue #130).
func eligible(dest *db.Destination) bool {
	return dest.RetentionEnabled && !dest.AppendOnly && dest.RetentionSchedule != ""
}

func (s *RetentionScheduler) addJob(dest *db.Destination) error {
	if !eligible(dest) {
		return nil
	}
	destID := dest.ID
	_, err := s.cron.NewJob(
		gocron.CronJob(dest.RetentionSchedule, false),
		gocron.NewTask(func() {
			ctx := context.Background()
			d, err := s.dests.GetByID(ctx, destID)
			if err != nil {
				s.logger.Warn("failed to reload destination for retention tick",
					zap.String("destination_id", destID.String()),
					zap.Error(err),
				)
				return
			}
			if !eligible(d) {
				return
			}
			if _, err := s.runJob(ctx, d); err != nil {
				s.logger.Warn("retention tick did not dispatch",
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

// AddDestination schedules a destination's retention sweep. Safe to call
// while the scheduler is running. Called by the destinations API handler
// after a destination is created or its retention config saved.
func (s *RetentionScheduler) AddDestination(dest *db.Destination) error {
	if err := s.addJob(dest); err != nil {
		return fmt.Errorf("failed to add destination %s to retention scheduler: %w", dest.ID, err)
	}
	s.logger.Info("destination added to retention scheduler",
		zap.String("destination_id", dest.ID.String()),
		zap.String("destination_name", dest.Name),
		zap.String("schedule", dest.RetentionSchedule),
	)
	return nil
}

// RemoveDestination removes a destination from the retention scheduler. Safe
// to call while the scheduler is running. Called on destination deletion.
func (s *RetentionScheduler) RemoveDestination(destinationID uuid.UUID) error {
	s.cron.RemoveByTags(destinationID.String())
	s.logger.Info("destination removed from retention scheduler", zap.String("destination_id", destinationID.String()))
	return nil
}

// UpdateDestination reschedules a destination after its retention schedule,
// enabled state, or append-only flag has changed. Removes the existing
// gocron job (if any) and adds a new one if the destination is still
// eligible.
func (s *RetentionScheduler) UpdateDestination(dest *db.Destination) error {
	s.cron.RemoveByTags(dest.ID.String())

	if !eligible(dest) {
		s.logger.Info("destination retention disabled, removed from scheduler",
			zap.String("destination_id", dest.ID.String()),
		)
		return nil
	}

	return s.AddDestination(dest)
}

// TriggerNow manually triggers an immediate retention sweep for a
// destination, bypassing its cron schedule. Used by the REST handler for an
// admin "run now" action. Returns the created Job so the caller can surface
// its ID, or an error if the sweep could not be dispatched (e.g. the
// retention agent is offline or a sweep is already queued) — unlike a
// scheduled tick, a manual trigger's caller needs to know it didn't happen.
// A busy destination is not an error: the sweep is queued and its Job
// returned (issue #285).
func (s *RetentionScheduler) TriggerNow(ctx context.Context, destinationID uuid.UUID) (*db.Job, error) {
	dest, err := s.dests.GetByID(ctx, destinationID)
	if err != nil {
		return nil, fmt.Errorf("destination not found: %w", err)
	}
	s.logger.Info("manual retention trigger requested",
		zap.String("destination_id", destinationID.String()),
		zap.String("destination_name", dest.Name),
	)
	return s.runJob(ctx, dest)
}

// runJob resolves the destination's live policies into restic tags, checks
// the retention agent is connected, then creates the Job/JobDestination
// records and starts the sweep — or queues it when the destination is busy
// (issue #285). Returns an error (with nothing persisted) if the agent is
// offline or a sweep of this destination is already pending, queued or
// running — the caller decides whether that is worth logging (a scheduled
// tick) or surfacing to a user (a manual trigger).
func (s *RetentionScheduler) runJob(ctx context.Context, dest *db.Destination) (*db.Job, error) {
	if dest.AppendOnly {
		return nil, fmt.Errorf("destination %s is append-only: retention cannot run", dest.ID)
	}
	if dest.RetentionAgentID == nil {
		return nil, fmt.Errorf("destination %s has no retention agent assigned", dest.ID)
	}
	agentID := *dest.RetentionAgentID

	tags, err := buildTags(ctx, s.dests, dest.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to build retention tags for destination %s: %w", dest.ID, err)
	}
	if len(tags) == 0 {
		return nil, fmt.Errorf("destination %s has no attached policies to sweep", dest.ID)
	}

	// Checked before anything is persisted: an offline agent means this tick
	// is simply skipped and retried on the next scheduled run — no pending
	// Job row, no retry queue (unlike the backup Scheduler's
	// createAndDispatch/DispatchPending).
	if !s.agentMgr.IsConnected(agentID.String()) {
		return nil, fmt.Errorf("retention agent %s is not connected", agentID)
	}

	// A sweep already queued or in flight does the same work: a second one
	// right behind it would only repeat it.
	active, err := s.jobs.HasActiveJobOfType(ctx, "retention", dest.ID)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, fmt.Errorf("a retention sweep of destination %s is already queued or running", dest.ID)
	}

	job := &db.Job{
		AgentID: agentID,
		Type:    "retention",
		Status:  "pending",
	}
	if err := s.jobs.Create(ctx, job); err != nil {
		return nil, fmt.Errorf("failed to create retention job record for destination %s: %w", dest.ID, err)
	}

	if err := s.jobs.CreateDestination(ctx, &db.JobDestination{
		JobID:         job.ID,
		DestinationID: dest.ID,
		Status:        "pending",
	}); err != nil {
		s.logger.Error("failed to create job destination record for retention job",
			zap.String("job_id", job.ID.String()),
			zap.String("destination_id", dest.ID.String()),
			zap.Error(err),
		)
	}

	err = s.start(ctx, job, dest, tags, s.queue.AdmitNew(job.ID))
	if errors.Is(err, destqueue.ErrNotAdmitted) {
		return job, s.queue.Enqueue(ctx, job.ID)
	}
	return job, err
}

// StartQueued implements destqueue.Starter for retention sweeps. The tags are
// resolved again, since policies may have been attached or detached while the
// sweep waited. A sweep whose destination was deleted, or whose retention
// settings no longer allow it, is closed as failed: the next scheduled tick
// creates a fresh one if retention still applies.
func (s *RetentionScheduler) StartQueued(ctx context.Context, job *db.Job, admit destqueue.AdmitFunc) error {
	destIDs, err := s.jobs.ListQueuedDestinationIDs(ctx, job.ID)
	if err != nil {
		return err
	}
	if len(destIDs) != 1 {
		return s.failQueued(ctx, job.ID, "the sweep has no destination left to run against")
	}
	dest, err := s.dests.GetByID(ctx, destIDs[0])
	if errors.Is(err, repositories.ErrNotFound) {
		return s.failQueued(ctx, job.ID, "the destination was deleted while this sweep was waiting")
	}
	if err != nil {
		return fmt.Errorf("failed to load destination %s: %w", destIDs[0], err)
	}
	if dest.AppendOnly || dest.RetentionAgentID == nil || *dest.RetentionAgentID != job.AgentID {
		return s.failQueued(ctx, job.ID, "the destination's retention settings changed while this sweep was waiting")
	}

	tags, err := buildTags(ctx, s.dests, dest.ID)
	if err != nil {
		return fmt.Errorf("failed to build retention tags for destination %s: %w", dest.ID, err)
	}
	if len(tags) == 0 {
		return s.failQueued(ctx, job.ID, "the destination has no attached policies to sweep")
	}

	return s.start(ctx, job, dest, tags, admit)
}

func (s *RetentionScheduler) failQueued(ctx context.Context, jobID uuid.UUID, errMsg string) error {
	now := time.Now().UTC()
	return s.jobs.UpdateStatus(ctx, jobID, "failed", nil, &now, errMsg)
}

// start claims the destination's busy gate through admit (issue #130), then
// creates the JobRetentionTag records and dispatches the sweep. When admit
// refuses, nothing is created or sent and destqueue.ErrNotAdmitted is
// returned.
func (s *RetentionScheduler) start(ctx context.Context, job *db.Job, dest *db.Destination, tags []string, admit destqueue.AdmitFunc) error {
	admitted, err := admit(ctx, []uuid.UUID{dest.ID})
	if err != nil {
		return fmt.Errorf("failed to acquire destination busy gate: %w", err)
	}
	if !admitted {
		return destqueue.ErrNotAdmitted
	}

	for _, tag := range tags {
		if err := s.jobs.CreateRetentionTag(ctx, &db.JobRetentionTag{
			JobID:         job.ID,
			DestinationID: dest.ID,
			Tag:           tag,
			Status:        "pending",
		}); err != nil {
			s.logger.Error("failed to create job retention tag record",
				zap.String("job_id", job.ID.String()),
				zap.String("tag", tag),
				zap.Error(err),
			)
		}
	}

	if err := s.dispatch(job, dest, job.AgentID, tags); err != nil {
		if relErr := s.dests.ReleaseBusy(ctx, dest.ID, job.ID); relErr != nil {
			s.logger.Error("failed to release destination busy gate after dispatch failure",
				zap.String("destination_id", dest.ID.String()),
				zap.Error(relErr),
			)
		}
		return fmt.Errorf("failed to dispatch retention job: %w", err)
	}

	return nil
}

// buildTags resolves a destination's live policies into the restic tags a
// retention sweep must cover: one bare "policy:<id>" tag per policy, plus one
// "policy:<id>:command:<name>" tag per that policy's command sources — the
// exact same tag set the backup Scheduler builds for a single policy's own
// dispatch (see scheduler.dispatch), just aggregated across every policy
// attached to this destination.
func buildTags(ctx context.Context, dests repositories.DestinationRepository, destinationID uuid.UUID) ([]string, error) {
	policies, err := dests.ListPoliciesByDestination(ctx, destinationID)
	if err != nil {
		return nil, err
	}

	tags := make([]string, 0, len(policies))
	for _, p := range policies {
		tags = append(tags, fmt.Sprintf("policy:%s", p.ID.String()))

		cmdSources, err := policyutil.CommandSources(p.Sources)
		if err != nil {
			return nil, fmt.Errorf("policy %s: failed to parse command sources: %w", p.ID, err)
		}
		for _, cs := range cmdSources {
			tags = append(tags, fmt.Sprintf("policy:%s:command:%s", p.ID.String(), cs.Name))
		}
	}
	return tags, nil
}

func (s *RetentionScheduler) dispatch(job *db.Job, dest *db.Destination, agentID uuid.UUID, tags []string) error {
	payload := retentionSweepPayload{
		Destination: destinationPayload{
			DestinationID: dest.ID.String(),
			Type:          dest.Type,
			RepoURL:       destutil.BuildRepoURL(dest),
			Credentials:   string(dest.Credentials),
			Config:        dest.Config,
			Env:           destutil.BuildEnv(dest),
		},
		RepoPassword: string(dest.RepoPassword),
		Retention: retentionPayload{
			Last:    dest.RetentionLast,
			Hourly:  dest.RetentionHourly,
			Daily:   dest.RetentionDaily,
			Weekly:  dest.RetentionWeekly,
			Monthly: dest.RetentionMonthly,
			Yearly:  dest.RetentionYearly,
		},
		Tags: tags,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal retention job payload: %w", err)
	}

	assignment := &proto.JobAssignment{
		JobId:       job.ID.String(),
		Type:        proto.JobType_JOB_TYPE_FORGET,
		Payload:     payloadBytes,
		ScheduledAt: timestamppb.Now(),
	}

	if err := s.agentMgr.Dispatch(agentID.String(), assignment); err != nil {
		return fmt.Errorf("agentmanager dispatch error: %w", err)
	}

	s.logger.Info("retention job dispatched",
		zap.String("job_id", job.ID.String()),
		zap.String("destination_id", dest.ID.String()),
		zap.String("agent_id", agentID.String()),
		zap.Int("tags", len(tags)),
	)
	return nil
}
