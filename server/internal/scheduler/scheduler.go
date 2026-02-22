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
	"fmt"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
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
	Sources        string               `json:"sources"`
	RepoPassword   string               `json:"repo_password"`
	Destinations   []destinationPayload `json:"destinations"`
	Retention      retentionPayload     `json:"retention"`
	HookPreBackup  string               `json:"hook_pre_backup"`
	HookPostBackup string               `json:"hook_post_backup"`
	Tags           []string             `json:"tags"`
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
	Daily   int `json:"daily"`
	Weekly  int `json:"weekly"`
	Monthly int `json:"monthly"`
	Yearly  int `json:"yearly"`
}

// Scheduler wraps gocron and coordinates job creation and dispatch.
// The zero value is not usable — create instances with New.
type Scheduler struct {
	cron     gocron.Scheduler
	policies repositories.PolicyRepository
	jobs     repositories.JobRepository
	dests    repositories.DestinationRepository
	agentMgr *agentmanager.Manager
	logger   *zap.Logger
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

		// Load policy and destinations to rebuild the full payload.
		// This is necessary because the job record alone does not carry
		// source paths, credentials, or retention settings.
		policy, destinations, err := s.policies.GetByIDWithDestinations(ctx, j.PolicyID)
		if err != nil {
			s.logger.Warn("failed to load policy for pending job dispatch",
				zap.String("job_id", j.ID.String()),
				zap.String("policy_id", j.PolicyID.String()),
				zap.Error(err),
			)
			continue
		}

		if err := s.dispatch(j, policy, destinations); err != nil {
			s.logger.Warn("failed to dispatch pending job to reconnected agent",
				zap.String("job_id", j.ID.String()),
				zap.String("agent_id", agentID.String()),
				zap.Error(err),
			)
		}
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
// via TriggerNow). It creates the Job and JobDestination DB records, updates
// policy timestamps, and dispatches the assignment to the agent.
func (s *Scheduler) runJob(policy *db.Policy, destinations []db.PolicyDestination) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if !policy.Enabled {
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
	if err := s.policies.UpdateSchedule(ctx, policy.ID, now, now); err != nil {
		// Non-fatal — the job was already created, just log the failure.
		s.logger.Warn("failed to update policy schedule timestamps",
			zap.String("policy_id", policy.ID.String()),
			zap.Error(err),
		)
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

	return nil
}

// dispatch builds a complete JobAssignment with the full backup payload and
// sends it to the agent via AgentManager. It loads full destination records
// (including decrypted credentials) so the agent has everything it needs
// without making additional calls back to the server.
func (s *Scheduler) dispatch(job *db.Job, policy *db.Policy, policyDests []db.PolicyDestination) error {
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
			RepoURL:       buildRepoURL(dest),
			Credentials:   string(dest.Credentials), // decrypted by EncryptedString scanner
			Config:        dest.Config,
			Env:           buildEnv(dest),
			Priority:      pd.Priority,
		})
	}

	payload := backupPayload{
		Sources:      policy.Sources,
		RepoPassword: string(policy.RepoPassword), // decrypted
		Destinations: destPayloads,
		Retention: retentionPayload{
			Daily:   policy.RetentionDaily,
			Weekly:  policy.RetentionWeekly,
			Monthly: policy.RetentionMonthly,
			Yearly:  policy.RetentionYearly,
		},
		HookPreBackup:  policy.HookPreBackup,
		HookPostBackup: policy.HookPostBackup,
		Tags:           []string{fmt.Sprintf("policy:%s", policy.ID.String())},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal job payload: %w", err)
	}

	assignment := &proto.JobAssignment{
		JobId:       job.ID.String(),
		PolicyId:    job.PolicyID.String(),
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

// buildRepoURL constructs the restic repository URL from a destination record.
// The format depends on the destination type and matches what restic expects.
func buildRepoURL(dest *db.Destination) string {
	switch dest.Type {
	case "local":
		var cfg struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(dest.Config), &cfg); err == nil && cfg.Path != "" {
			return cfg.Path
		}
	case "s3":
		var cfg struct {
			Bucket   string `json:"bucket"`
			Endpoint string `json:"endpoint"`
			Path     string `json:"path"`
		}
		if err := json.Unmarshal([]byte(dest.Config), &cfg); err == nil && cfg.Bucket != "" {
			endpoint := cfg.Endpoint
			if endpoint == "" {
				endpoint = "s3.amazonaws.com"
			}
			path := cfg.Path
			if path == "" {
				path = "/"
			}
			return fmt.Sprintf("s3:%s/%s%s", endpoint, cfg.Bucket, path)
		}
	case "sftp":
		var cfg struct {
			Host string `json:"host"`
			User string `json:"user"`
			Path string `json:"path"`
			Port int    `json:"port"`
		}
		if err := json.Unmarshal([]byte(dest.Config), &cfg); err == nil && cfg.Host != "" {
			user := ""
			if cfg.User != "" {
				user = cfg.User + "@"
			}
			port := ""
			if cfg.Port != 0 && cfg.Port != 22 {
				port = fmt.Sprintf(":%d", cfg.Port)
			}
			return fmt.Sprintf("sftp:%s%s%s:%s", user, cfg.Host, port, cfg.Path)
		}
	case "rest":
		var cfg struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal([]byte(dest.Config), &cfg); err == nil && cfg.URL != "" {
			return fmt.Sprintf("rest:%s", cfg.URL)
		}
	case "rclone":
		var cfg struct {
			Remote string `json:"remote"`
		}
		if err := json.Unmarshal([]byte(dest.Config), &cfg); err == nil && cfg.Remote != "" {
			return fmt.Sprintf("rclone:%s", cfg.Remote)
		}
	}
	return ""
}

// buildEnv derives backend-specific environment variables from a destination.
// For S3, AWS credentials are extracted from the Credentials JSON.
// For rclone, the credentials JSON is a flat map of RCLONE_CONFIG_* env vars.
func buildEnv(dest *db.Destination) map[string]string {
	env := make(map[string]string)
	if dest.Credentials == "" {
		return env
	}

	creds := string(dest.Credentials)

	switch dest.Type {
	case "s3":
		var c struct {
			AccessKeyID     string `json:"access_key_id"`
			SecretAccessKey string `json:"secret_access_key"`
			Region          string `json:"region"`
		}
		if err := json.Unmarshal([]byte(creds), &c); err == nil {
			if c.AccessKeyID != "" {
				env["AWS_ACCESS_KEY_ID"] = c.AccessKeyID
			}
			if c.SecretAccessKey != "" {
				env["AWS_SECRET_ACCESS_KEY"] = c.SecretAccessKey
			}
			if c.Region != "" {
				env["AWS_DEFAULT_REGION"] = c.Region
			}
		}
	case "rclone":
		var c map[string]string
		if err := json.Unmarshal([]byte(creds), &c); err == nil {
			for k, v := range c {
				env[k] = v
			}
		}
	}

	return env
}