// Package scheduler manages the lifecycle of backup jobs triggered by policy
// schedules. It wraps gocron and integrates with PolicyRepository (to load and
// update policies), JobRepository (to persist job records), DestinationRepository
// (to load credentials for dispatch), and AgentManager (to dispatch jobs to
// connected agents via the open gRPC stream).
//
// Each policy maps to exactly one gocron job, identified by the policy UUID.
// Jobs run in singleton mode: if a policy's previous job is still running when
// the next tick fires, the new execution is skipped to avoid overlapping backups.
//
// Dispatch flow:
//  1. Tick fires → create Job + JobDestination records in DB (status: pending)
//  2. Build a JobAssignment proto with the full backup payload (sources,
//     destinations with decrypted credentials, retention, hooks)
//  3. Attempt immediate dispatch via AgentManager if agent is connected
//  4. If agent is offline, the job stays pending; DispatchPending retries
//     when the agent reconnects (called from the gRPC server on StreamJobs open)
package scheduler

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
	"github.com/arkeep-io/arkeep/server/internal/destutil"
	"github.com/arkeep-io/arkeep/server/internal/notification"
	"github.com/arkeep-io/arkeep/server/internal/policyutil"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// backupPayload is the JSON-encoded payload embedded in a JobAssignment
// for JOB_TYPE_BACKUP jobs. The agent deserializes this to get everything
// it needs to execute the backup without additional server calls.
//
// Credentials are included in plaintext — they are decrypted by the server
// before dispatch. The gRPC channel provides transport security.
// The agent must never log or expose these values.
type backupPayload struct {
	Sources         string                 `json:"sources"`
	RepoPassword    string                 `json:"repo_password"`
	Destinations    []destinationPayload   `json:"destinations"`
	Retention       retentionPayload       `json:"retention"`
	HookPreBackup   string                 `json:"hook_pre_backup"`
	HookPostBackup  string                 `json:"hook_post_backup"`
	Tags            []string               `json:"tags"`
	ExcludePatterns []string               `json:"exclude_patterns"`
	CommandSources  []commandSourcePayload `json:"command_sources"`
}

// destinationPayload carries the resolved details of a single backup target.
// RepoURL is pre-built by the server so the agent does not need to construct
// restic URLs from raw config fields.
type destinationPayload struct {
	DestinationID string            `json:"destination_id"`
	Type          string            `json:"type"`
	RepoURL       string            `json:"repo_url"`
	Credentials   string            `json:"credentials"`
	Config        string            `json:"config"`
	Env           map[string]string `json:"env"`
	Priority      int               `json:"priority"`
}

// retentionPayload mirrors the keep_* fields from db.Policy.
type retentionPayload struct {
	Last    int `json:"last"`
	Hourly  int `json:"hourly"`
	Daily   int `json:"daily"`
	Weekly  int `json:"weekly"`
	Monthly int `json:"monthly"`
	Yearly  int `json:"yearly"`
}

// commandSourcePayload is one command-type source: the agent runs it as its
// own restic backup --stdin-from-command invocation, producing its own
// snapshot.
type commandSourcePayload struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	// Tags is this source's own retention pool. It deliberately does NOT
	// include the bare policy:<id> tag: restic's forget --tag filter matches
	// snapshots *containing* the tag, so a shared tag would let the regular
	// pool's forget sweep this source's snapshots (and vice versa).
	Tags []string `json:"tags"`
}

// ErrPolicyDisabled is returned by TriggerNow when the target policy is disabled.
var ErrPolicyDisabled = errors.New("policy is disabled")

// Scheduler wraps gocron and coordinates job creation and dispatch.
// The zero value is not usable — create instances with New.
type Scheduler struct {
	cron     gocron.Scheduler
	policies repositories.PolicyRepository
	jobs     repositories.JobRepository
	dests    repositories.DestinationRepository
	agentMgr *agentmanager.Manager
	logger   *zap.Logger
	running  atomic.Bool

	// notifSvc may be nil. Set via SetNotificationService after construction,
	// because the notification service is built after the scheduler (it needs the
	// WebSocket hub). Only used to report that automatic resume gave up.
	notifSvc notification.Service
}

// SetNotificationService attaches the notification service. Safe to skip: the
// scheduler only uses it to tell the user that a repeatedly interrupted backup
// will no longer be resumed automatically.
func (s *Scheduler) SetNotificationService(svc notification.Service) {
	s.notifSvc = svc
}

// New creates and configures a new Scheduler. Call Start to begin processing.
func New(
	policies repositories.PolicyRepository,
	jobs repositories.JobRepository,
	dests repositories.DestinationRepository,
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
		dests:    dests,
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
			s.logger.Error("failed to schedule policy",
				zap.String("policy_id", enabled[i].ID.String()),
				zap.String("policy_name", enabled[i].Name),
				zap.Error(err),
			)
		}
	}

	s.logger.Info("scheduler started", zap.Int("policies_scheduled", len(enabled)))
	s.cron.Start()
	s.running.Store(true)
	return nil
}

// Stop gracefully shuts down the underlying gocron scheduler, waiting for any
// currently running job functions to complete before returning.
func (s *Scheduler) Stop() error {
	if err := s.cron.Shutdown(); err != nil {
		return fmt.Errorf("scheduler shutdown error: %w", err)
	}
	s.running.Store(false)
	s.logger.Info("scheduler stopped")
	return nil
}

// IsRunning reports whether the scheduler has been started and not yet stopped.
// Used by the /health/ready endpoint.
func (s *Scheduler) IsRunning() bool {
	return s.running.Load()
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
	s.logger.Info("policy removed from scheduler", zap.String("policy_id", policyID.String()))
	return nil
}

// UpdatePolicy reschedules a policy after its cron expression or enabled state
// has changed. Removes the existing gocron job and adds a new one.
func (s *Scheduler) UpdatePolicy(policy *db.Policy) error {
	s.cron.RemoveByTags(policy.ID.String())

	if !policy.Enabled {
		s.logger.Info("policy disabled, removed from scheduler",
			zap.String("policy_id", policy.ID.String()),
		)
		return nil
	}

	return s.AddPolicy(policy)
}

// TriggerNow manually triggers an immediate job run for a policy, bypassing
// the cron schedule. Used by the REST handler for on-demand backups.
// It returns the created Job so the caller can surface its ID to the client.
func (s *Scheduler) TriggerNow(ctx context.Context, policyID uuid.UUID) (*db.Job, error) {
	policy, destinations, err := s.policies.GetByIDWithDestinations(ctx, policyID)
	if err != nil {
		return nil, fmt.Errorf("policy not found: %w", err)
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
	pendingJobs, err := s.jobs.ListByAgentAndStatus(ctx, agentID, "pending", opts)
	if err != nil {
		s.logger.Error("failed to fetch pending jobs for agent",
			zap.String("agent_id", agentID.String()),
			zap.Error(err),
		)
		return
	}

	for i := range pendingJobs {
		j := &pendingJobs[i]

		// A job without a policy is a restore of an imported snapshot: there is
		// no policy to rebuild a backup payload from, and it is not this
		// method's job to re-dispatch it.
		if j.PolicyID == nil {
			continue
		}

		// Load policy and destinations to rebuild the full payload.
		// This is necessary because the job record alone does not carry
		// source paths, credentials, or retention settings.
		policy, destinations, err := s.policies.GetByIDWithDestinations(ctx, *j.PolicyID)
		if err != nil {
			s.logger.Warn("failed to load policy for pending job dispatch",
				zap.String("job_id", j.ID.String()),
				zap.String("policy_id", j.PolicyID.String()),
				zap.Error(err),
			)
			continue
		}

		if err := s.dispatch(&j.Job, policy, destinations); err != nil {
			s.logger.Warn("failed to dispatch pending job to reconnected agent",
				zap.String("job_id", j.ID.String()),
				zap.String("agent_id", agentID.String()),
				zap.Error(err),
			)
		}
	}
}

// maxResumeAttempts caps how many times in a row a backup is resumed after being
// interrupted. Without a cap, a machine that always dies at the same point would
// be retried on every reconnection forever; with it, the user is told once that
// automatic resume gave up.
const maxResumeAttempts = 3

// ResumeInterrupted queues a fresh run for each backup of this agent that was
// interrupted by a disconnection. Called by the gRPC server when an agent
// reconnects, right after DispatchPending.
//
// The interrupted job keeps its own record; a new job is created that points back
// to it. Resuming is cheap: restic keeps the packs already uploaded, so the new
// run only transfers what is missing.
//
// Skips are logged with a reason — a resume that silently does not happen is
// indistinguishable from a bug.
func (s *Scheduler) ResumeInterrupted(ctx context.Context, agentID uuid.UUID) {
	opts := repositories.ListOptions{Limit: 100, Offset: 0}
	interrupted, err := s.jobs.ListByAgentAndStatus(ctx, agentID, "interrupted", opts)
	if err != nil {
		s.logger.Error("failed to fetch interrupted jobs for agent",
			zap.String("agent_id", agentID.String()),
			zap.Error(err),
		)
		return
	}

	for i := range interrupted {
		j := &interrupted[i]
		jobField := zap.String("job_id", j.ID.String())

		// Restores are user-initiated and write to a chosen target path; silently
		// re-running one on reconnect would be surprising.
		if j.Type != "backup" {
			continue
		}

		policy, destinations, err := s.policies.GetByIDWithDestinations(ctx, *j.PolicyID)
		if err != nil {
			s.logger.Info("not resuming interrupted job: policy unavailable",
				jobField,
				zap.String("policy_id", j.PolicyID.String()),
				zap.Error(err),
			)
			continue
		}
		if !policy.Enabled {
			s.logger.Info("not resuming interrupted job: policy is disabled", jobField)
			continue
		}
		if !policy.ResumeInterrupted {
			s.logger.Info("not resuming interrupted job: resume disabled on the policy", jobField)
			continue
		}

		// A later run of the same policy already covers this data, so resuming
		// would be duplicate work. This is also what stops an already-resumed job
		// from being picked up again on every subsequent reconnection.
		superseded, err := s.jobs.HasJobForPolicyAfter(ctx, *j.PolicyID, j.CreatedAt)
		if err != nil {
			s.logger.Warn("could not check for a newer run of the policy, not resuming",
				jobField,
				zap.Error(err),
			)
			continue
		}
		if superseded {
			s.logger.Info("not resuming interrupted job: a newer run of the policy exists", jobField)
			continue
		}

		if j.ResumeAttempt >= maxResumeAttempts {
			s.giveUpOnResume(ctx, j, policy)
			continue
		}

		if _, err := s.createAndDispatch(policy, destinations, &j.Job); err != nil {
			s.logger.Warn("failed to resume interrupted job",
				jobField,
				zap.Error(err),
			)
		}
	}
}

// giveUpOnResume closes out an interrupted job that has exhausted its resume
// attempts: it becomes "failed" with an explanatory message, which also takes it
// out of the set ResumeInterrupted scans, and the user is notified once.
func (s *Scheduler) giveUpOnResume(ctx context.Context, j *repositories.JobWithNames, policy *db.Policy) {
	errMsg := fmt.Sprintf("backup interrupted %d times in a row; automatic resume gave up", j.ResumeAttempt+1)

	if err := s.jobs.MarkResumeExhausted(ctx, j.ID, errMsg); err != nil {
		// Another reconnection got there first; the notification was sent then.
		if errors.Is(err, repositories.ErrNotFound) {
			return
		}
		s.logger.Warn("failed to close out a job whose resume attempts are exhausted",
			zap.String("job_id", j.ID.String()),
			zap.Error(err),
		)
		return
	}

	s.logger.Warn("giving up on resuming a repeatedly interrupted backup",
		zap.String("job_id", j.ID.String()),
		zap.String("policy_id", policy.ID.String()),
		zap.Int("attempts", j.ResumeAttempt+1),
	)

	if s.notifSvc == nil {
		return
	}
	if err := s.notifSvc.NotifyJobFailed(ctx, j.ID, policy.ID, policy.Name, errMsg); err != nil {
		s.logger.Warn("failed to send resume-exhausted notification",
			zap.String("job_id", j.ID.String()),
			zap.Error(err),
		)
	}
}

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

			if _, err := s.runJob(&p, destinations); err != nil && !errors.Is(err, ErrPolicyDisabled) {
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
// via TriggerNow). It creates the Job and JobDestination DB records, updates
// policy timestamps, and dispatches the assignment to the agent.
// It returns the created Job so callers can surface its ID.
func (s *Scheduler) runJob(policy *db.Policy, destinations []repositories.PolicyDestinationWithName) (*db.Job, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// If a pending job already exists for this policy, skip creating another one.
	// The existing pending job will be dispatched when the agent reconnects.
	// Only guards scheduled/manual runs: a resume (see createAndDispatch) must
	// not be skipped just because an unrelated pending job exists for the policy.
	hasPending, err := s.jobs.HasPendingJob(ctx, policy.ID)
	if err != nil {
		s.logger.Error("failed to check pending jobs for policy",
			zap.String("policy_id", policy.ID.String()),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to check pending jobs for policy %s: %w", policy.ID, err)
	}
	if hasPending {
		s.logger.Debug("skipping scheduled backup: pending job already exists for policy",
			zap.String("policy_id", policy.ID.String()),
			zap.String("policy_name", policy.Name),
		)
		return nil, nil
	}

	return s.createAndDispatch(policy, destinations, nil)
}

// createAndDispatch is the body of runJob. resumeOf, when non-nil, is the
// interrupted job this run continues: the new job records the provenance and the
// attempt counter, and the policy's schedule timestamps are left alone, because a
// resume is not a scheduled execution and must not move the next run.
func (s *Scheduler) createAndDispatch(policy *db.Policy, destinations []repositories.PolicyDestinationWithName, resumeOf *db.Job) (*db.Job, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if !policy.Enabled {
		s.logger.Info("skipping job for disabled policy",
			zap.String("policy_id", policy.ID.String()),
		)
		return nil, ErrPolicyDisabled
	}

	// --- Create Job record ---
	job := &db.Job{
		PolicyID: &policy.ID,
		AgentID:  policy.AgentID,
		Status:   "pending",
	}
	if resumeOf != nil {
		job.ResumeOfJobID = &resumeOf.ID
		job.ResumeAttempt = resumeOf.ResumeAttempt + 1
	}
	if err := s.jobs.Create(ctx, job); err != nil {
		return nil, fmt.Errorf("failed to create job record for policy %s: %w", policy.ID, err)
	}

	fields := []zap.Field{
		zap.String("job_id", job.ID.String()),
		zap.String("policy_id", policy.ID.String()),
		zap.String("policy_name", policy.Name),
		zap.String("agent_id", policy.AgentID.String()),
	}
	if resumeOf != nil {
		fields = append(fields,
			zap.String("resume_of_job_id", resumeOf.ID.String()),
			zap.Int("resume_attempt", job.ResumeAttempt),
		)
	}
	s.logger.Info("job created", fields...)

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
	// Skipped for a resume: it is a continuation of an earlier scheduled run, so
	// it must not shift the policy's last/next run.
	if resumeOf == nil {
		now := time.Now().UTC()
		if err := s.policies.UpdateSchedule(ctx, policy.ID, now, now); err != nil {
			// Non-fatal — the job was already created, just log the failure.
			s.logger.Warn("failed to update policy schedule timestamps",
				zap.String("policy_id", policy.ID.String()),
				zap.Error(err),
			)
		}
	}

	// --- Dispatch to agent ---
	if err := s.dispatch(job, policy, destinations); err != nil {
		// Non-fatal: the job is persisted as pending. DispatchPending will
		// retry when the agent reconnects.
		s.logger.Warn("dispatch failed, job remains pending",
			zap.String("job_id", job.ID.String()),
			zap.String("agent_id", policy.AgentID.String()),
			zap.Error(err),
		)
	}

	return job, nil
}

// dispatch builds a complete JobAssignment with the full backup payload and
// sends it to the agent via AgentManager. It loads full destination records
// (including decrypted credentials) so the agent has everything it needs
// without making additional calls back to the server.
func (s *Scheduler) dispatch(job *db.Job, policy *db.Policy, policyDests []repositories.PolicyDestinationWithName) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	destPayloads := make([]destinationPayload, 0, len(policyDests))
	for _, pd := range policyDests {
		dest, err := s.dests.GetByID(ctx, pd.DestinationID)
		if err != nil {
			s.logger.Error("failed to load destination for dispatch",
				zap.String("destination_id", pd.DestinationID.String()),
				zap.Error(err),
			)
			continue
		}
		destPayloads = append(destPayloads, destinationPayload{
			DestinationID: dest.ID.String(),
			Type:          dest.Type,
			RepoURL:       destutil.BuildRepoURL(dest),
			Credentials:   string(dest.Credentials), // decrypted by EncryptedString scanner
			Config:        dest.Config,
			Env:           destutil.BuildEnv(dest),
			Priority:      pd.Priority,
		})
	}

	sourcePaths, err := policyutil.SourcePaths(policy.Sources)
	if err != nil {
		return fmt.Errorf("failed to build sources list: %w", err)
	}
	sourcesFlatBytes, err := json.Marshal(sourcePaths)
	if err != nil {
		return fmt.Errorf("failed to marshal sources list: %w", err)
	}
	sourcesFlat := string(sourcesFlatBytes)

	commandSources, err := policyutil.CommandSources(policy.Sources)
	if err != nil {
		return fmt.Errorf("failed to build command sources list: %w", err)
	}
	cmdPayloads := make([]commandSourcePayload, 0, len(commandSources))
	for _, cs := range commandSources {
		cmdPayloads = append(cmdPayloads, commandSourcePayload{
			Name:    cs.Name,
			Command: cs.Command,
			Tags:    []string{fmt.Sprintf("policy:%s:command:%s", policy.ID.String(), cs.Name)},
		})
	}

	var excludePatterns []string
	if policy.ExcludePatterns != "" && policy.ExcludePatterns != "[]" {
		if err := json.Unmarshal([]byte(policy.ExcludePatterns), &excludePatterns); err != nil {
			s.logger.Warn("failed to parse exclude_patterns, ignoring",
				zap.String("policy_id", policy.ID.String()),
				zap.Error(err),
			)
		}
	}

	payload := backupPayload{
		Sources:      sourcesFlat,
		RepoPassword: string(policy.RepoPassword), // decrypted
		Destinations: destPayloads,
		Retention: retentionPayload{
			Last:    policy.RetentionLast,
			Hourly:  policy.RetentionHourly,
			Daily:   policy.RetentionDaily,
			Weekly:  policy.RetentionWeekly,
			Monthly: policy.RetentionMonthly,
			Yearly:  policy.RetentionYearly,
		},
		HookPreBackup:   policy.HookPreBackup,
		HookPostBackup:  policy.HookPostBackup,
		Tags:            []string{fmt.Sprintf("policy:%s", policy.ID.String())},
		ExcludePatterns: excludePatterns,
		CommandSources:  cmdPayloads,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal job payload: %w", err)
	}

	assignment := &proto.JobAssignment{
		JobId:       job.ID.String(),
		PolicyId:    policy.ID.String(),
		Type:        proto.JobType_JOB_TYPE_BACKUP,
		Payload:     payloadBytes,
		ScheduledAt: timestamppb.Now(),
	}

	if err := s.agentMgr.Dispatch(job.AgentID.String(), assignment); err != nil {
		return fmt.Errorf("agentmanager dispatch error: %w", err)
	}

	s.logger.Info("job dispatched",
		zap.String("job_id", job.ID.String()),
		zap.String("agent_id", job.AgentID.String()),
		zap.Int("destinations", len(destPayloads)),
	)
	return nil
}

