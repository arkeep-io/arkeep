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
	// Raw is the original JSON line, forwarded as-is to the log stream.
	Raw          string  `json:"-"`
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

// Backup runs a restic backup for the given destination and sources.
// Progress events are forwarded to onProgress as they arrive on stdout.
// onProgress may be nil if the caller does not need live progress.
//
// Returns an error if the backup fails. A non-zero restic exit code is
// always wrapped in the returned error with the stderr output included.
func (w *Wrapper) Backup(ctx context.Context, dest Destination, opts BackupOptions, onProgress ProgressFunc) error {
	args := []string{"backup", "--json"}

	for _, tag := range opts.Tags {
		args = append(args, "--tag", tag)
	}
	for _, ex := range opts.ExcludePatterns {
		args = append(args, "--exclude", ex)
	}
	args = append(args, opts.Sources...)

	return w.runWithProgress(ctx, dest, args, onProgress)
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
func (w *Wrapper) Restore(ctx context.Context, dest Destination, snapshotID, targetDir string, includePath string) error {
	args := []string{"restore", snapshotID, "--target", targetDir}
	if includePath != "" {
		args = append(args, "--include", includePath)
	}
	return w.run(ctx, dest, args)
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
				cmd.Process.Kill()
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