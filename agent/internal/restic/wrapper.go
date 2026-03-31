// wrapper.go provides the Wrapper type, the single public interface through
// which the executor interacts with the backup engine. All restic and rclone
// invocations are encapsulated here — no other package may call these binaries
// directly.
//
// Design notes:
//   - Each method maps to one logical backup operation (backup, forget, check,
//     snapshots, restore). Internally, some operations may combine restic with
//     rclone depending on the destination type.
//   - Progress events from restic --json are parsed and forwarded to a
//     ProgressFunc callback so the caller can stream them to the server via
//     gRPC without coupling the wrapper to the transport layer.
//   - The Wrapper is safe for concurrent use — each method call creates an
//     independent exec.Cmd with its own stdout/stderr pipes.
package restic

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// DestinationType identifies the storage backend for a destination.
// It maps directly to the db.Destination.Type field on the server.
type DestinationType string

const (
	DestLocal  DestinationType = "local"
	DestS3     DestinationType = "s3"
	DestSFTP   DestinationType = "sftp"
	DestRest   DestinationType = "rest"
	DestRclone DestinationType = "rclone" // catch-all for rclone-only providers
)

// Destination describes a single backup target passed to Wrapper methods.
// Credentials and Config are JSON-decoded from the db.Destination fields
// before being passed here — the Wrapper works with already-decrypted values.
type Destination struct {
	Type     DestinationType
	// RepoURL is the restic repository URL, pre-formatted for the destination
	// type (e.g. "s3:s3.amazonaws.com/bucket", "sftp:user@host:/path",
	// ":local:/mnt/backup"). For rclone destinations this is the rclone remote
	// path (e.g. "gdrive:backups/myserver").
	RepoURL  string
	Password string // restic repository password (decrypted)
	// Env holds extra environment variables required by the backend driver
	// (e.g. AWS_ACCESS_KEY_ID, RCLONE_CONFIG_*). These are added to the
	// subprocess environment alongside the standard restic variables.
	Env      map[string]string
}

// BackupOptions carries the parameters for a backup run.
type BackupOptions struct {
	// Sources is the list of paths or docker-volume references to back up.
	Sources  []string
	// Tags are attached to the resulting snapshot for filtering.
	Tags     []string
	// ExcludePatterns are passed to restic as --exclude flags.
	ExcludePatterns []string
}

// SnapshotInfo holds the metadata of a single snapshot returned by restic.
type SnapshotInfo struct {
	ID       string   `json:"id"`
	Time     string   `json:"time"`
	Paths    []string `json:"paths"`
	Tags     []string `json:"tags"`
	Hostname string   `json:"hostname"`
	Username string   `json:"username"`
	// ShortID is the 8-character abbreviated snapshot ID.
	ShortID  string   `json:"short_id"`
}

// RetentionPolicy mirrors the keep_* fields from db.Policy.
type RetentionPolicy struct {
	Daily   int
	Weekly  int
	Monthly int
	Yearly  int
}

// ProgressEvent represents a single JSON event emitted by restic --json.
// Only the fields relevant to progress reporting are decoded; the rest are
// ignored. The raw JSON line is also preserved so callers can forward it
// verbatim to the server log stream.
type ProgressEvent struct {
	// MessageType is "status", "summary", or "error" for backup;
	// "check-ok" or "check-error" for check operations.
	MessageType  string  `json:"message_type"`
	PercentDone  float64 `json:"percent_done"`
	FilesNew     uint64  `json:"files_new"`
	FilesDone    uint64  `json:"files_done"`
	BytesDone    uint64  `json:"bytes_done"`
	TotalFiles   uint64  `json:"total_files"`
	TotalBytes   uint64  `json:"total_bytes"`

	// Summary-only fields — only present when MessageType == "summary".
	// SnapshotID is the full SHA256 ID of the snapshot created by this backup run.
	SnapshotID          string `json:"snapshot_id"`
	// TotalBytesProcessed is the total size of all source files examined.
	TotalBytesProcessed uint64 `json:"total_bytes_processed"`
	// DataAdded is the number of new bytes added to the repository (deduplicated).
	DataAdded           uint64 `json:"data_added"`

	// Raw is the original JSON line, forwarded as-is to the log stream.
	Raw string `json:"-"`
}

// BackupResult holds the outcome of a completed backup run, extracted from
// the restic --json summary event. It is returned by Backup() alongside any
// error so the caller can report per-destination metrics to the server.
type BackupResult struct {
	// SnapshotID is the full ID of the snapshot created by this run.
	// Empty if the backup failed before creating a snapshot.
	SnapshotID string
	// TotalBytesProcessed is the total size of all files examined.
	TotalBytesProcessed uint64
	// DataAdded is the net bytes added to the repository after deduplication.
	DataAdded uint64
}

// ProgressFunc is called for each progress event emitted during a long-running
// operation. Returning an error from ProgressFunc cancels the operation.
// It is always called from the same goroutine that reads restic's stdout, so
// implementations must not block for long.
type ProgressFunc func(event ProgressEvent) error

// Wrapper executes backup operations using the embedded restic and rclone
// binaries. Create instances with NewWrapper.
type Wrapper struct {
	resticBin string // absolute path to the extracted restic binary
	rcloneBin string // absolute path to the extracted rclone binary
}

// NewWrapper extracts the embedded binaries (if needed) and returns a ready
// Wrapper. This should be called once at agent startup.
func NewWrapper(extractor *Extractor) (*Wrapper, error) {
	resticBin, err := extractor.ResticPath()
	if err != nil {
		return nil, fmt.Errorf("restic: failed to prepare restic binary: %w", err)
	}

	rcloneBin, err := extractor.RclonePath()
	if err != nil {
		return nil, fmt.Errorf("restic: failed to prepare rclone binary: %w", err)
	}

	return &Wrapper{
		resticBin: resticBin,
		rcloneBin: rcloneBin,
	}, nil
}

// Init initialises the restic repository at dest if it does not exist yet.
// Idempotent: if the repository is already initialised the error is silenced.
func (w *Wrapper) Init(ctx context.Context, dest Destination) error {
	err := w.run(ctx, dest, []string{"init"})
	if err != nil && strings.Contains(err.Error(), "already") {
		return nil
	}
	return err
}

// Backup runs a restic backup for the given destination and sources.
// Progress events are forwarded to onProgress as they arrive on stdout.
// onProgress may be nil if the caller does not need live progress.
//
// Returns a BackupResult with snapshot metadata extracted from the restic
// summary event, and an error if the backup fails. A non-zero restic exit
// code is always wrapped in the returned error with stderr included.
func (w *Wrapper) Backup(ctx context.Context, dest Destination, opts BackupOptions, onProgress ProgressFunc) (*BackupResult, error) {
	if err := w.Init(ctx, dest); err != nil {
		return nil, fmt.Errorf("restic: failed to init repository: %w", err)
	}

	args := []string{"backup", "--json"}

	for _, tag := range opts.Tags {
		args = append(args, "--tag", tag)
	}
	for _, ex := range opts.ExcludePatterns {
		args = append(args, "--exclude", ex)
	}
	args = append(args, opts.Sources...)

	var result BackupResult

	// Wrap the caller's onProgress to intercept the summary event and extract
	// snapshot metadata. The summary event is the last event emitted by restic
	// on successful completion — it carries snapshot_id and byte counts.
	intercepted := func(ev ProgressEvent) error {
		if ev.MessageType == "summary" {
			result.SnapshotID = ev.SnapshotID
			result.TotalBytesProcessed = ev.TotalBytesProcessed
			result.DataAdded = ev.DataAdded
		}
		if onProgress != nil {
			return onProgress(ev)
		}
		return nil
	}

	if err := w.runWithProgress(ctx, dest, args, intercepted); err != nil {
		return nil, err
	}
	return &result, nil
}

// Forget runs restic forget --prune to apply the retention policy.
// It removes snapshot metadata and frees storage in a single pass.
func (w *Wrapper) Forget(ctx context.Context, dest Destination, policy RetentionPolicy) error {
	args := []string{
		"forget", "--prune", "--json",
		"--keep-daily", fmt.Sprintf("%d", policy.Daily),
		"--keep-weekly", fmt.Sprintf("%d", policy.Weekly),
		"--keep-monthly", fmt.Sprintf("%d", policy.Monthly),
		"--keep-yearly", fmt.Sprintf("%d", policy.Yearly),
	}
	return w.run(ctx, dest, args)
}

// Check verifies the integrity of the repository. Progress events (one per
// pack file checked) are forwarded to onProgress.
func (w *Wrapper) Check(ctx context.Context, dest Destination, onProgress ProgressFunc) error {
	args := []string{"check", "--json"}
	return w.runWithProgress(ctx, dest, args, onProgress)
}

// Snapshots returns the list of snapshots stored in the repository.
func (w *Wrapper) Snapshots(ctx context.Context, dest Destination) ([]SnapshotInfo, error) {
	args := []string{"snapshots", "--json", "--no-lock"}

	out, err := w.output(ctx, dest, args)
	if err != nil {
		return nil, err
	}

	var snapshots []SnapshotInfo
	if err := json.Unmarshal(out, &snapshots); err != nil {
		return nil, fmt.Errorf("restic: failed to parse snapshots output: %w", err)
	}
	return snapshots, nil
}

// Restore restores a snapshot (or a path within it) to targetDir.
// snapshotID may be "latest" to restore the most recent snapshot.
// includePath, if non-empty, limits restoration to a sub-path inside the snapshot.
// excludePaths lists paths to skip during restore (e.g. read-only volume mounts).
// hostRoot, if non-empty, is the container path where the host filesystem is
// bind-mounted (e.g. "/hostfs"). When set, lchown failures on paths under
// hostRoot are silently tolerated: they occur because the host filesystem
// (typically Windows NTFS) does not support Unix ownership operations, but the
// file data is restored correctly. When empty (native Linux/Windows deployments),
// all errors are propagated as-is.
func (w *Wrapper) Restore(ctx context.Context, dest Destination, snapshotID, targetDir string, includePath string, excludePaths []string, hostRoot string) error {
	args := []string{"restore", snapshotID, "--target", targetDir, "--json"}
	if includePath != "" {
		args = append(args, "--include", includePath)
	}
	for _, ex := range excludePaths {
		args = append(args, "--exclude", ex)
	}
	return w.runRestoreJSON(ctx, dest, args, hostRoot)
}

// runRestoreJSON runs restic restore --json, consuming stdout as a JSON event
// stream (to prevent pipe stalls) and capturing stderr separately.
// On exit error, only stderr is inspected for lchown failures — mixing JSON
// stdout with text stderr via CombinedOutput would risk false-positive matches.
// lchown tolerance is applied only when hostRoot is non-empty (Docker deployments
// with a bind-mounted host filesystem); on native binaries hostRoot is "" and
// all errors are propagated unchanged.
func (w *Wrapper) runRestoreJSON(ctx context.Context, dest Destination, args []string, hostRoot string) error {
	cmd := w.buildCmd(ctx, dest, args)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("restic: failed to open stdout pipe: %w", err)
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("restic: failed to start: %w", err)
	}

	// Drain stdout to prevent the subprocess from blocking when its pipe buffer
	// fills up. We ignore the JSON content for now — restore progress is not
	// currently streamed to the UI.
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		// discard
	}

	if err := cmd.Wait(); err != nil {
		stderr := strings.TrimSpace(stderrBuf.String())
		if hostRoot != "" && isOnlyHostRootLchownErrors(stderr, hostRoot) {
			return nil
		}
		return fmt.Errorf("restic: command failed: %w\n%s", err, stderr)
	}
	return nil
}

// isOnlyHostRootLchownErrors returns true when every error restic encountered
// was an lchown/permission-denied failure on a directory that lives under
// hostRoot. This pattern occurs during in-place restores when the snapshot
// paths include host-OS mount point ancestors (e.g. /hostfs/c, /hostfs/c/Users)
// that the container cannot chown because the underlying filesystem (Windows
// NTFS) does not support Unix ownership. File data is intact in these cases.
// Errors on paths outside hostRoot are always treated as real failures.
func isOnlyHostRootLchownErrors(stderr, hostRoot string) bool {
	foundLchownError := false
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// "ignoring error for /hostfs/c: lchown /hostfs/c: permission denied"
		if strings.HasPrefix(line, "ignoring error for ") {
			isLchown := strings.Contains(line, "lchown") && strings.Contains(line, "permission denied")
			isUnderHostRoot := strings.Contains(line, hostRoot)
			if isLchown && isUnderHostRoot {
				foundLchownError = true
				continue
			}
			return false // non-lchown error or path outside hostRoot → real problem
		}
		// "Fatal: There were 2 errors" — acceptable when all errors are lchown
		if strings.HasPrefix(line, "Fatal: There were ") && strings.HasSuffix(line, "errors") {
			continue
		}
		if strings.HasPrefix(line, "Fatal:") || strings.HasPrefix(line, "Error:") {
			return false
		}
	}
	return foundLchownError
}

// run executes a restic command and waits for it to finish.
// stderr is captured and included in the error if the command fails.
func (w *Wrapper) run(ctx context.Context, dest Destination, args []string) error {
	cmd := w.buildCmd(ctx, dest, args)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("restic: command failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// output executes a restic command and returns its stdout as raw bytes.
func (w *Wrapper) output(ctx context.Context, dest Destination, args []string) ([]byte, error) {
	cmd := w.buildCmd(ctx, dest, args)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Errorf("restic: command failed: %w\n%s", err, stderr)
	}
	return out, nil
}

// runWithProgress executes a restic command, reading stdout line by line and
// parsing each line as a JSON progress event. Each event is forwarded to
// onProgress if non-nil. Stderr is collected and included in the error on
// failure.
//
// restic --json emits newline-delimited JSON objects on stdout. Each object
// has a "message_type" field that identifies the event kind. Non-JSON lines
// (e.g. deprecation warnings) are logged at debug level and skipped.
func (w *Wrapper) runWithProgress(ctx context.Context, dest Destination, args []string, onProgress ProgressFunc) error {
	cmd := w.buildCmd(ctx, dest, args)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("restic: failed to open stdout pipe: %w", err)
	}

	// Collect stderr separately so it can be included in error messages.
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("restic: failed to start: %w", err)
	}

	// Read stdout line by line, parsing each as a JSON progress event.
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var event ProgressEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			// Non-JSON line (e.g. a warning from an older restic version) —
			// skip silently. The raw line is not forwarded to avoid noise.
			continue
		}
		event.Raw = line

		if onProgress != nil {
			if err := onProgress(event); err != nil {
				// Caller signalled cancellation — kill the process.
				// Ignore the kill error: the process may have already exited.
				_ = cmd.Process.Kill()
				return fmt.Errorf("restic: progress callback cancelled: %w", err)
			}
		}
	}

	if err := cmd.Wait(); err != nil {
		stderr := strings.TrimSpace(stderrBuf.String())
		return fmt.Errorf("restic: command failed: %w\n%s", err, stderr)
	}
	return nil
}

// buildCmd constructs the exec.Cmd for a restic invocation.
// It sets RESTIC_REPOSITORY, RESTIC_PASSWORD, and any backend-specific
// environment variables from dest.Env. For rclone destinations it also
// passes the rclone binary path via RCLONE_BINARY so restic can find it.
func (w *Wrapper) buildCmd(ctx context.Context, dest Destination, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, w.resticBin, args...)

	// Build environment: start from the current process env so that PATH,
	// HOME, and system variables are inherited, then overlay restic-specific
	// variables. This avoids having to enumerate every variable the OS needs.
	env := append(cmd.Environ(),
		"RESTIC_REPOSITORY="+dest.RepoURL,
		"RESTIC_PASSWORD="+dest.Password,
	)

	// For rclone-backed destinations, tell restic where the rclone binary is.
	// restic uses the rclone backend transparently when the repo URL starts
	// with "rclone:".
	if dest.Type == DestRclone {
		env = append(env, "RCLONE_BINARY="+w.rcloneBin)
	}

	for k, v := range dest.Env {
		env = append(env, k+"="+v)
	}

	cmd.Env = env
	return cmd
}