// Package executor manages the agent's job queue and dispatches backup jobs
// to the appropriate handlers. It sits between the connection manager (which
// receives job assignments from the server via gRPC) and the restic wrapper,
// docker discovery, and hooks runner (which do the actual work).
//
// The executor runs one job at a time (sequential execution) to avoid
// concurrent restic processes competing for I/O on the same machine.
// The server is aware of this constraint and does not dispatch a second job
// to an agent that already has one running.
//
// Interfaces:
//   - LogSink: implemented by the connection manager, receives log lines
//     produced during execution and forwards them to the server via StreamLogs.
//   - StatusReporter: implemented by the connection manager, receives job
//     lifecycle transitions and forwards them via ReportJobStatus.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/agent/internal/docker"
	"github.com/arkeep-io/arkeep/agent/internal/hooks"
	"github.com/arkeep-io/arkeep/agent/internal/restic"
)

// LogSink receives log lines produced during job execution and forwards them
// to the server. Implemented by the connection manager.
type LogSink interface {
	SendLog(jobID, level, message string)
}

// StatusReporter receives job lifecycle transitions and forwards them to the
// server. Implemented by the connection manager.
type StatusReporter interface {
	ReportStatus(jobID, status, message string)
}

// JobAssignment is the internal representation of a job received from the server.
// Payload is the raw JSON bytes from the proto message — the executor
// deserializes it according to the job type during execution.
type JobAssignment struct {
	JobID    string
	PolicyID string
	Payload  []byte
}

// backupPayload mirrors the struct serialized by the server scheduler.
// All credentials arrive already decrypted — the server handles encryption
// at rest, the gRPC channel handles transport security.
type backupPayload struct {
	Sources        string               `json:"sources"`
	RepoPassword   string               `json:"repo_password"`
	Destinations   []destinationPayload `json:"destinations"`
	Retention      retentionPayload     `json:"retention"`
	HookPreBackup  string               `json:"hook_pre_backup"`
	HookPostBackup string               `json:"hook_post_backup"`
	Tags           []string             `json:"tags"`
}

type destinationPayload struct {
	DestinationID string            `json:"destination_id"`
	Type          string            `json:"type"`
	RepoURL       string            `json:"repo_url"`
	Credentials   string            `json:"credentials"`
	Config        string            `json:"config"`
	Env           map[string]string `json:"env"`
	Priority      int               `json:"priority"`
}

type retentionPayload struct {
	Daily   int `json:"daily"`
	Weekly  int `json:"weekly"`
	Monthly int `json:"monthly"`
	Yearly  int `json:"yearly"`
}

// queueSize is the maximum number of jobs that can be buffered in the channel
// while waiting to be executed. Jobs beyond this limit are rejected — the
// server will retry them on the next reconnect via DispatchPending.
const queueSize = 16

// Executor receives job assignments, queues them, and executes them one at a
// time using the restic wrapper, docker client, and hooks runner.
type Executor struct {
	wrapper *restic.Wrapper
	docker  *docker.Client // may be nil if Docker is unavailable on this host
	hooks   *hooks.Runner
	queue   chan JobAssignment
	logger  *zap.Logger
}

// New creates a new Executor. dockerClient may be nil — if it is, any job
// that requires Docker volume discovery will fail gracefully.
func New(
	wrapper *restic.Wrapper,
	dockerClient *docker.Client,
	hooksRunner *hooks.Runner,
	logger *zap.Logger,
) *Executor {
	return &Executor{
		wrapper: wrapper,
		docker:  dockerClient,
		hooks:   hooksRunner,
		queue:   make(chan JobAssignment, queueSize),
		logger:  logger.Named("executor"),
	}
}

// Run starts the worker loop. It blocks until ctx is cancelled, processing
// one job at a time from the queue.
// sink and reporter are provided here (not at construction) so they can be
// the connection manager itself, which is created after the executor.
func (e *Executor) Run(ctx context.Context, sink LogSink, reporter StatusReporter) {
	e.logger.Info("executor started")
	for {
		select {
		case <-ctx.Done():
			e.logger.Info("executor stopped")
			return
		case job := <-e.queue:
			e.execute(ctx, job, sink, reporter)
		}
	}
}

// Enqueue adds a job to the queue. Returns an error if the queue is full.
// Non-blocking — the caller should log and discard rejected jobs; the server
// will retry via DispatchPending on the next reconnect.
func (e *Executor) Enqueue(job JobAssignment) error {
	select {
	case e.queue <- job:
		e.logger.Info("job enqueued",
			zap.String("job_id", job.JobID),
			zap.String("policy_id", job.PolicyID),
		)
		return nil
	default:
		return fmt.Errorf("executor: job queue full, rejecting job %s", job.JobID)
	}
}

// execute runs a single job to completion. It deserializes the payload,
// resolves sources, runs hooks, and calls the restic wrapper.
//
// Execution sequence:
//  1. Deserialize payload
//  2. Report status "running"
//  3. Resolve docker-volume:// sources to host mountpoints
//  4. Run pre-backup hook (abort on failure)
//  5. For each destination: run restic backup, stream progress, run forget
//  6. Run post-backup hook (non-fatal, always runs)
//  7. Report status "success" or "failed"
func (e *Executor) execute(ctx context.Context, job JobAssignment, sink LogSink, reporter StatusReporter) {
	log := func(level, msg string) {
		sink.SendLog(job.JobID, level, msg)
		switch level {
		case "error":
			e.logger.Error(msg, zap.String("job_id", job.JobID))
		case "warn":
			e.logger.Warn(msg, zap.String("job_id", job.JobID))
		default:
			e.logger.Info(msg, zap.String("job_id", job.JobID))
		}
	}

	fail := func(msg string) {
		log("error", msg)
		reporter.ReportStatus(job.JobID, "failed", msg)
	}

	// --- 1. Deserialize payload ---
	var payload backupPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		fail(fmt.Sprintf("failed to deserialize job payload: %v", err))
		return
	}

	// --- 2. Report running ---
	reporter.ReportStatus(job.JobID, "running", "starting backup")
	log("info", "backup started")

	// --- 3. Resolve sources ---
	sources, err := e.resolveSources(ctx, payload.Sources, log)
	if err != nil {
		fail(fmt.Sprintf("failed to resolve backup sources: %v", err))
		return
	}
	log("info", fmt.Sprintf("resolved %d source(s)", len(sources)))

	// --- 4. Pre-backup hook ---
	if payload.HookPreBackup != "" {
		log("info", fmt.Sprintf("running pre-backup hook: %s", payload.HookPreBackup))
		result, err := e.hooks.Run(ctx, payload.HookPreBackup)
		if result.Output != "" {
			log("info", "pre-backup hook output: "+result.Output)
		}
		if err != nil {
			fail(fmt.Sprintf("pre-backup hook failed (exit %d): %v", result.ExitCode, err))
			return
		}
	}

	// --- 5. Backup to each destination ---
	backupFailed := false
	for _, dest := range payload.Destinations {
		if dest.RepoURL == "" {
			log("warn", fmt.Sprintf("destination %s has empty repo_url, skipping", dest.DestinationID))
			continue
		}

		log("info", fmt.Sprintf("backing up to destination %s (type: %s)", dest.DestinationID, dest.Type))

		d := restic.Destination{
			Type:     restic.DestinationType(dest.Type),
			RepoURL:  dest.RepoURL,
			Password: payload.RepoPassword,
			Env:      dest.Env,
		}

		opts := restic.BackupOptions{
			Sources: sources,
			Tags:    payload.Tags,
		}

		err := e.wrapper.Backup(ctx, d, opts, func(ev restic.ProgressEvent) error {
			if data, err := json.Marshal(ev); err == nil {
				sink.SendLog(job.JobID, "info", string(data))
			}
			return nil
		})
		if err != nil {
			log("error", fmt.Sprintf("backup to destination %s failed: %v", dest.DestinationID, err))
			backupFailed = true
			continue
		}

		log("info", fmt.Sprintf("backup to destination %s completed", dest.DestinationID))

		// Apply retention policy — non-fatal if it fails (backup data is safe).
		retention := restic.RetentionPolicy{
			Daily:   payload.Retention.Daily,
			Weekly:  payload.Retention.Weekly,
			Monthly: payload.Retention.Monthly,
			Yearly:  payload.Retention.Yearly,
		}
		if err := e.wrapper.Forget(ctx, d, retention); err != nil {
			log("warn", fmt.Sprintf("retention policy failed for destination %s: %v", dest.DestinationID, err))
		}
	}

	// --- 6. Post-backup hook (always runs) ---
	if payload.HookPostBackup != "" {
		log("info", fmt.Sprintf("running post-backup hook: %s", payload.HookPostBackup))
		result, _ := e.hooks.Run(ctx, payload.HookPostBackup)
		if result.Output != "" {
			log("info", "post-backup hook output: "+result.Output)
		}
	}

	// --- 7. Final status ---
	if backupFailed {
		fail("one or more destinations failed")
		return
	}

	log("info", "backup completed successfully")
	reporter.ReportStatus(job.JobID, "success", "backup completed")
}

// resolveSources parses the JSON sources array and resolves any
// docker-volume:// entries to their host mountpoints.
// Returns the final list of filesystem paths ready for restic.
func (e *Executor) resolveSources(ctx context.Context, sourcesJSON string, log func(level, msg string)) ([]string, error) {
	var raw []string
	if err := json.Unmarshal([]byte(sourcesJSON), &raw); err != nil {
		return nil, fmt.Errorf("invalid sources JSON: %w", err)
	}

	resolved := make([]string, 0, len(raw))
	for _, src := range raw {
		if !strings.HasPrefix(src, "docker-volume://") {
			resolved = append(resolved, src)
			continue
		}

		// Docker volume source: docker-volume://<volume-name>
		volumeName := strings.TrimPrefix(src, "docker-volume://")

		if e.docker == nil {
			return nil, fmt.Errorf("source %q requires Docker but Docker is unavailable on this host", src)
		}

		info, err := e.docker.InspectVolume(ctx, volumeName)
		if err != nil {
			return nil, fmt.Errorf("failed to inspect Docker volume %q: %w", volumeName, err)
		}

		log("info", fmt.Sprintf("resolved docker-volume://%s → %s", volumeName, info.Mountpoint))
		resolved = append(resolved, info.Mountpoint)
	}

	return resolved, nil
}