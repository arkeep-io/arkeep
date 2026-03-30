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
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/agent/internal/docker"
	"github.com/arkeep-io/arkeep/agent/internal/hooks"
	"github.com/arkeep-io/arkeep/agent/internal/restic"
	proto "github.com/arkeep-io/arkeep/shared/proto"
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
	// ReportDestinationResult reports the outcome of a backup to a single
	// destination. Called once per destination after it completes or fails.
	// sizeBytes is TotalBytesProcessed from the restic summary event.
	ReportDestinationResult(jobID, destinationID, status, snapshotID string, startedAt time.Time, sizeBytes int64, errMsg string)
}

// JobAssignment is the internal representation of a job received from the server.
// Payload is the raw JSON bytes from the proto message — the executor
// deserializes it according to the job type during execution.
type JobAssignment struct {
	JobID    string
	PolicyID string
	Type     proto.JobType
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

// restorePayload mirrors the struct serialized by the server snapshot handler.
// All credentials arrive already decrypted.
type restorePayload struct {
	ResticSnapshotID string             `json:"restic_snapshot_id"`
	RepoPassword     string             `json:"repo_password"`
	TargetPath       string             `json:"target_path"`
	Destination      destinationPayload `json:"destination"`
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
	wrapper        *restic.Wrapper
	docker         *docker.Client // may be nil if Docker is unavailable on this host
	hooks          *hooks.Runner
	queue          chan JobAssignment
	logger         *zap.Logger
	dockerHostRoot string // ARKEEP_DOCKER_HOST_ROOT — when set, local paths are translated to this prefix
}

// New creates a new Executor. dockerClient may be nil — if it is, any job
// that requires Docker volume discovery will fail gracefully.
// dockerHostRoot is the value of ARKEEP_DOCKER_HOST_ROOT: when non-empty, any
// local destination path or restore target path entered by the user is
// automatically translated so it resolves inside the container. This lets
// users enter native host paths (e.g. C:/Users/… on Windows or /home/user/…
// on Linux) without having to pre-configure per-directory bind-mounts.
func New(
	wrapper *restic.Wrapper,
	dockerClient *docker.Client,
	hooksRunner *hooks.Runner,
	logger *zap.Logger,
	dockerHostRoot string,
) *Executor {
	return &Executor{
		wrapper:        wrapper,
		docker:         dockerClient,
		hooks:          hooksRunner,
		queue:          make(chan JobAssignment, queueSize),
		logger:         logger.Named("executor"),
		dockerHostRoot: dockerHostRoot,
	}
}

// translateLocalPath maps a user-provided filesystem path to the corresponding
// container-accessible path when ARKEEP_DOCKER_HOST_ROOT is set.
//
// Examples (hostRoot = "/hostfs"):
//
//	/home/user/backups         → /hostfs/home/user/backups
//	C:/Users/Filippo/Downloads → /hostfs/c/Users/Filippo/Downloads
//	C:\Users\Filippo\Downloads → /hostfs/c/Users/Filippo/Downloads
//
// If hostRoot is empty the path is returned unchanged (non-Docker deployments
// or the legacy /arkeep-backups bind-mount approach).
func translateLocalPath(path, hostRoot string) string {
	if hostRoot == "" {
		return path
	}
	// Already under the host root — avoid double-translation.
	if strings.HasPrefix(filepath.ToSlash(path), filepath.ToSlash(hostRoot)+"/") {
		return path
	}
	// Windows-style path: C:\… or C:/…
	if len(path) >= 2 && path[1] == ':' {
		drive := strings.ToLower(string(path[0]))
		rest := filepath.ToSlash(path[2:]) // strip drive letter + colon, normalise separators
		if !strings.HasPrefix(rest, "/") {
			rest = "/" + rest
		}
		return filepath.Join(hostRoot, drive+rest)
	}
	// Unix absolute path.
	if strings.HasPrefix(path, "/") {
		return filepath.Join(hostRoot, path)
	}
	// Relative path — no translation.
	return path
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
			zap.String("type", job.Type.String()),
		)
		return nil
	default:
		return fmt.Errorf("executor: job queue full, rejecting job %s", job.JobID)
	}
}

// execute routes a job to the appropriate handler based on its type.
func (e *Executor) execute(ctx context.Context, job JobAssignment, sink LogSink, reporter StatusReporter) {
	switch job.Type {
	case proto.JobType_JOB_TYPE_RESTORE:
		e.executeRestore(ctx, job, sink, reporter)
	default:
		// JOB_TYPE_BACKUP and unspecified types all run the backup handler.
		e.executeBackup(ctx, job, sink, reporter)
	}
}

// executeBackup runs a single backup job to completion.
//
// Execution sequence:
//  1. Deserialize payload
//  2. Report status "running"
//  3. Resolve docker-volume:// sources to host mountpoints
//  4. Run pre-backup hook (abort on failure)
//  5. For each destination: run restic backup, stream progress, run forget
//  6. Run post-backup hook (non-fatal, always runs)
//  7. Report status "succeeded" or "failed"
func (e *Executor) executeBackup(ctx context.Context, job JobAssignment, sink LogSink, reporter StatusReporter) {
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
	if len(sources) == 0 {
		fail("no accessible backup sources: all docker-volume mountpoints are unreachable on this host. " +
			"If running a native agent on Windows, Docker volume paths are not directly accessible. " +
			"Use the Docker-based agent deployment to back up Docker volumes.")
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

		// Record the start time before invoking restic so the server can persist
		// an accurate started_at on the JobDestination row.
		destStartedAt := time.Now().UTC()

		// For local destinations, translate the user-provided path to the
		// container-accessible path (when ARKEEP_DOCKER_HOST_ROOT is set), then
		// ensure the directory exists and is writable before handing off to
		// restic. This produces a clear, actionable error instead of the
		// cryptic "permission denied" from restic internals.
		if dest.Type == "local" {
			dest.RepoURL = translateLocalPath(dest.RepoURL, e.dockerHostRoot)
			if err := os.MkdirAll(dest.RepoURL, 0755); err != nil {
				errMsg := fmt.Sprintf(
					"local path %q is not writable: %v — "+
						"if running inside Docker, set ARKEEP_DOCKER_HOST_ROOT and mount the host "+
						"filesystem (e.g. /:/hostfs:rw on Linux, C:/:/hostfs/c:rw on Windows), "+
						"or set PUID/PGID to match the directory owner",
					dest.RepoURL, err,
				)
				log("error", fmt.Sprintf("backup to destination %s failed: %s", dest.DestinationID, errMsg))
				reporter.ReportDestinationResult(job.JobID, dest.DestinationID, "failed", "", destStartedAt, 0, errMsg)
				backupFailed = true
				continue
			}
		}

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

		result, err := e.wrapper.Backup(ctx, d, opts, func(ev restic.ProgressEvent) error {
			if data, err := json.Marshal(ev); err == nil {
				sink.SendLog(job.JobID, "info", string(data))
			}
			return nil
		})
		if err != nil {
			errMsg := fmt.Sprintf("backup to destination %s failed: %v", dest.DestinationID, err)
			log("error", errMsg)
			reporter.ReportDestinationResult(job.JobID, dest.DestinationID, "failed", "", destStartedAt, 0, err.Error())
			backupFailed = true
			continue
		}

		log("info", fmt.Sprintf("backup to destination %s completed (snapshot: %s, size: %d bytes)",
			dest.DestinationID, result.SnapshotID, result.TotalBytesProcessed))

		reporter.ReportDestinationResult(
			job.JobID,
			dest.DestinationID,
			"succeeded",
			result.SnapshotID,
			destStartedAt,
			int64(result.TotalBytesProcessed),
			"",
		)

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

// executeRestore runs a single restore job to completion.
//
// Execution sequence:
//  1. Deserialize payload
//  2. Report status "running"
//  3. Run restic restore, streaming output as log lines
//  4. Report status "succeeded" or "failed"
func (e *Executor) executeRestore(ctx context.Context, job JobAssignment, sink LogSink, reporter StatusReporter) {
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
	var payload restorePayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		fail(fmt.Sprintf("failed to deserialize restore payload: %v", err))
		return
	}

	if payload.ResticSnapshotID == "" {
		fail("restore payload missing restic_snapshot_id")
		return
	}
	if payload.TargetPath == "" {
		fail("restore payload missing target_path")
		return
	}
	if payload.Destination.RepoURL == "" {
		fail("restore payload missing destination repo_url")
		return
	}

	// --- 2. Report running ---
	reporter.ReportStatus(job.JobID, "running", "starting restore")

	// Translate the restore target path when ARKEEP_DOCKER_HOST_ROOT is set,
	// the same way backup destination paths are translated.
	targetPath := translateLocalPath(payload.TargetPath, e.dockerHostRoot)
	log("info", fmt.Sprintf("restore started: snapshot %s → %s", payload.ResticSnapshotID, targetPath))

	// --- 3. Run restore ---
	d := restic.Destination{
		Type:     restic.DestinationType(payload.Destination.Type),
		RepoURL:  payload.Destination.RepoURL,
		Password: payload.RepoPassword,
		Env:      payload.Destination.Env,
	}

	if err := e.wrapper.Restore(ctx, d, payload.ResticSnapshotID, targetPath, ""); err != nil {
		if strings.Contains(err.Error(), "Access is denied") {
			// On Windows, restic cannot set timestamps or file attributes on
			// system-protected directories reconstructed under the target path.
			// Files are restored correctly — log as warning and continue.
			log("warn", "restore completed with warnings: some file metadata could not be set (Windows permission restriction)")
			log("warn", "files were restored successfully — check the target path to verify")
		} else if strings.Contains(err.Error(), "read-only file system") {
			// Restore-in-place to Docker named volumes fails because the agent
			// mounts /var/lib/docker/volumes read-only for backup safety.
			// To restore in place: either mount volumes read-write in docker-compose
			// (remove :ro), stop the affected containers first, then re-run the
			// restore. Alternatively, restore to a separate directory and copy
			// the files manually.
			fail("restore failed: one or more target paths are on a read-only filesystem. " +
				"When restoring Docker volume data in place, the agent needs /var/lib/docker/volumes " +
				"mounted read-write (remove :ro from the docker-compose volume entry) and the " +
				"affected containers must be stopped before restoring. " +
				"As an alternative, restore to a separate directory and copy the files from there.")
			return
		} else {
			fail(fmt.Sprintf("restore failed: %v", err))
			return
		}
	}

	// --- 4. Final status ---
	log("info", fmt.Sprintf("restore completed successfully: files written to %s", targetPath))
	reporter.ReportStatus(job.JobID, "success", "restore completed")
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

		// Skip the volume if its mountpoint is not accessible from this host.
		// On a native Windows agent, Docker Desktop returns Linux-style paths
		// (e.g. /var/lib/docker/volumes/…/_data) that live inside the WSL2 VM
		// and are not reachable from the Windows filesystem. In a Docker-based
		// deployment the host volume root must be bind-mounted into the agent
		// container (e.g. - /var/lib/docker/volumes:/var/lib/docker/volumes:ro).
		if _, statErr := os.Stat(info.Mountpoint); statErr != nil {
			log("error", fmt.Sprintf(
				"docker-volume://%s resolved to %q but that path is not accessible on this host — "+
					"skipping source. If running in Docker, add a read-only bind mount: "+
					"- %s:%s:ro",
				volumeName, info.Mountpoint, info.Mountpoint, info.Mountpoint,
			))
			continue
		}

		resolved = append(resolved, info.Mountpoint)
	}

	return resolved, nil
}