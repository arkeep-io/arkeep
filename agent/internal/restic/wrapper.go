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
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"go.uber.org/zap"
)

// cancelGracePeriod is how long a restic subprocess is given to exit after
// receiving SIGINT (see buildCmd) before Go escalates to SIGKILL. Kept well
// under systemd's default TimeoutStopSec (30s) so a clean shutdown always has
// a chance to complete before the service manager forces a kill.
const cancelGracePeriod = 15 * time.Second

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

// LsEntry represents a single file or directory returned by `restic ls --json`.
type LsEntry struct {
	Path  string `json:"path"`
	Type  string `json:"type"` // "file" or "dir"
	Size  int64  `json:"size"`
	Mtime string `json:"mtime"`
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
	// Summary carries the per-snapshot statistics restic records since 0.17.
	// Absent for snapshots written by older versions, leaving the fields zero.
	Summary SnapshotSummary `json:"summary"`
}

// SnapshotSummary holds the subset of a snapshot's stored statistics that is
// needed to report its size, as emitted by `restic snapshots --json`.
type SnapshotSummary struct {
	// DataAddedPacked is the footprint the snapshot added to the repository
	// after compression and deduplication — the same metric recorded for
	// snapshots produced by a backup.
	DataAddedPacked int64 `json:"data_added_packed"`
	// TotalFilesProcessed is the number of files the snapshot covered.
	TotalFilesProcessed int64 `json:"total_files_processed"`
}

// RetentionPolicy mirrors the keep_* fields from db.Policy.
type RetentionPolicy struct {
	Last    int
	Hourly  int
	Daily   int
	Weekly  int
	Monthly int
	Yearly  int
}

// IsEnabled reports whether the policy has at least one non-zero keep rule.
// A zero policy means retention is disabled; Forget should be skipped entirely.
func (p RetentionPolicy) IsEnabled() bool {
	return p.Last > 0 || p.Hourly > 0 || p.Daily > 0 ||
		p.Weekly > 0 || p.Monthly > 0 || p.Yearly > 0
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
	// DataAdded is the number of new bytes added to the repository (deduplicated,
	// before compression).
	DataAdded           uint64 `json:"data_added"`
	// DataAddedPacked is the compressed bytes actually written to the repository
	// by this backup (deduplicated + compressed) — the real incremental footprint.
	DataAddedPacked     uint64 `json:"data_added_packed"`

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
	// DataAddedPacked is the compressed bytes actually written to the repository
	// by this run — the real incremental footprint on disk.
	DataAddedPacked uint64
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
	logger    *zap.Logger
}

// NewWrapper extracts the embedded binaries (if needed) and returns a ready
// Wrapper. This should be called once at agent startup.
func NewWrapper(extractor *Extractor, logger *zap.Logger) (*Wrapper, error) {
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
		logger:    logger.Named("restic"),
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

// buildBackupArgs constructs the restic backup argument slice for the given
// options. goos mirrors runtime.GOOS and is a parameter so the function can
// be tested without cross-compiling.
//
// Sources are appended after a "--" end-of-options marker so that a source
// path beginning with "-" (e.g. "--password-command=...") can never be
// interpreted by restic as a flag: without the marker, restic accepts global
// flags after the backup subcommand, and --password-command in particular
// runs an arbitrary shell command to obtain the repository password.
func buildBackupArgs(opts BackupOptions, goos string) []string {
	args := []string{"backup", "--json"}
	if goos == "windows" {
		// VSS creates a consistent snapshot of locked/in-use files.
		// Requires the agent to run with Administrator privileges.
		args = append(args, "--use-fs-snapshot")
	}
	for _, tag := range opts.Tags {
		args = append(args, "--tag", tag)
	}
	for _, ex := range opts.ExcludePatterns {
		args = append(args, "--exclude", ex)
	}
	args = append(args, "--")
	args = append(args, opts.Sources...)
	return args
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

	args := buildBackupArgs(opts, runtime.GOOS)

	var result BackupResult

	// Wrap the caller's onProgress to intercept the summary event and extract
	// snapshot metadata. The summary event is the last event emitted by restic
	// on successful completion — it carries snapshot_id and byte counts.
	intercepted := func(ev ProgressEvent) error {
		if ev.MessageType == "summary" {
			result.SnapshotID = ev.SnapshotID
			result.TotalBytesProcessed = ev.TotalBytesProcessed
			result.DataAdded = ev.DataAdded
			result.DataAddedPacked = ev.DataAddedPacked
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
//
// tags scopes the retention to the snapshots that carry them, so a policy only
// ever prunes its own snapshots. Without it restic considers every snapshot in
// the repository, and a destination shared by two policies would have both
// pruned by whichever policy ran last — effectively applying the most
// aggressive retention to everything. Callers pass the same tags used for the
// backup, so the filter is symmetric by construction. tags must not be empty:
// see buildForgetArgs.
func (w *Wrapper) Forget(ctx context.Context, dest Destination, policy RetentionPolicy, tags []string) error {
	if !policy.IsEnabled() {
		return nil
	}
	args, err := buildForgetArgs(policy, tags)
	if err != nil {
		return err
	}
	return w.run(ctx, dest, args)
}

// buildForgetArgs assembles the restic forget arguments for a retention policy
// scoped to tags. It returns an error when tags is empty rather than falling
// back to an unscoped forget: pruning nothing is recoverable, pruning another
// policy's snapshots is not.
func buildForgetArgs(policy RetentionPolicy, tags []string) ([]string, error) {
	if len(tags) == 0 {
		return nil, fmt.Errorf("restic: refusing to run forget without tags: an unscoped forget would prune snapshots belonging to other policies")
	}
	args := []string{"forget", "--prune", "--json"}
	for _, tag := range tags {
		args = append(args, "--tag", tag)
	}
	if policy.Last > 0 {
		args = append(args, "--keep-last", fmt.Sprintf("%d", policy.Last))
	}
	if policy.Hourly > 0 {
		args = append(args, "--keep-hourly", fmt.Sprintf("%d", policy.Hourly))
	}
	if policy.Daily > 0 {
		args = append(args, "--keep-daily", fmt.Sprintf("%d", policy.Daily))
	}
	if policy.Weekly > 0 {
		args = append(args, "--keep-weekly", fmt.Sprintf("%d", policy.Weekly))
	}
	if policy.Monthly > 0 {
		args = append(args, "--keep-monthly", fmt.Sprintf("%d", policy.Monthly))
	}
	if policy.Yearly > 0 {
		args = append(args, "--keep-yearly", fmt.Sprintf("%d", policy.Yearly))
	}
	return args, nil
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

// Restore restores a snapshot (or specific paths within it) to targetDir.
// snapshotID may be "latest" to restore the most recent snapshot.
// includePaths, if non-empty, limits restoration to those sub-paths inside the
// snapshot (each maps to a separate restic --include flag).
// excludePaths lists paths to skip during restore (e.g. read-only volume mounts).
// hostRoot, if non-empty, is the container path where the host filesystem is
// bind-mounted (e.g. "/hostfs"). When set, lchown failures on paths under
// hostRoot are silently tolerated: they occur because the host filesystem
// (typically Windows NTFS) does not support Unix ownership operations, but the
// file data is restored correctly. When empty (native Linux/Windows deployments),
// all errors are propagated as-is.
func (w *Wrapper) Restore(ctx context.Context, dest Destination, snapshotID, targetDir string, includePaths []string, excludePaths []string, hostRoot string) error {
	args := buildRestoreArgs(snapshotID, targetDir, includePaths, excludePaths)
	return w.runRestoreJSON(ctx, dest, args, hostRoot)
}

// buildRestoreArgs constructs the restic restore argument slice. snapshotID
// is appended last, after a "--" end-of-options marker, so a snapshot ID (or
// "latest") can never be interpreted by restic as a flag — see
// buildBackupArgs for why this matters.
func buildRestoreArgs(snapshotID, targetDir string, includePaths []string, excludePaths []string) []string {
	args := []string{"restore", "--target", targetDir, "--json"}
	for _, p := range includePaths {
		args = append(args, "--include", p)
	}
	for _, ex := range excludePaths {
		args = append(args, "--exclude", ex)
	}
	args = append(args, "--", snapshotID)
	return args
}

// Ls returns the direct children of dir within snapshotID (non-recursive).
// It runs `restic ls --json <snapshotID> <dir>` and parses the newline-delimited
// JSON output. Without --recursive, restic lists only the immediate entries of
// dir, which keeps the response small and fast even on huge snapshots (the GUI
// expands one directory level at a time). The snapshot header line and the dir
// entry itself are skipped, so the result is exactly the directory's children.
// dir defaults to "/" (the snapshot root) when empty.
//
// The "--" end-of-options marker separates flags from the positional snapshotID
// and dir, so a dir that begins with "-" (it originates from a user-supplied
// ?path= query) can never be interpreted by restic as a flag.
func (w *Wrapper) Ls(ctx context.Context, dest Destination, snapshotID, dir string) ([]LsEntry, error) {
	if dir == "" {
		dir = "/"
	}
	cmd := w.buildCmd(ctx, dest, []string{"ls", "--json", "--", snapshotID, dir})
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("restic: failed to open stdout pipe: %w", err)
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("restic: failed to start: %w", err)
	}

	var entries []LsEntry
	scanner := bufio.NewScanner(stdout)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if strings.Contains(string(line), `"struct_type":"snapshot"`) {
			continue
		}
		var raw struct {
			Path  string `json:"path"`
			Type  string `json:"type"`
			Size  int64  `json:"size"`
			Mtime string `json:"mtime"`
		}
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		if raw.Path == dir {
			// restic emits the queried directory itself; skip it so the result
			// contains only its direct children.
			continue
		}
		entries = append(entries, LsEntry{
			Path:  raw.Path,
			Type:  raw.Type,
			Size:  raw.Size,
			Mtime: raw.Mtime,
		})
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("restic: error reading ls output: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		stderr := strings.TrimSpace(stderrBuf.String())
		return nil, fmt.Errorf("restic: command failed: %w\n%s", err, stderr)
	}
	return entries, nil
}

// StatsResult holds the real on-disk footprint of a restic repository.
type StatsResult struct {
	// TotalSize is the deduplicated, compressed size of all data stored in the
	// repository (total_size from `restic stats --mode raw-data`), i.e. the
	// actual disk usage — not the logical size of the backed-up files.
	TotalSize uint64
}

// Stats returns the repository's real on-disk size via
// `restic stats --mode raw-data --json`. Used to report per-destination usage.
// restic prints a progress line alongside the JSON on stdout, so the output is
// scanned for the JSON object line rather than unmarshalled wholesale.
func (w *Wrapper) Stats(ctx context.Context, dest Destination) (StatsResult, error) {
	cmd := w.buildCmd(ctx, dest, []string{"stats", "--mode", "raw-data", "--json"})
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return StatsResult{}, fmt.Errorf("restic: failed to open stdout pipe: %w", err)
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return StatsResult{}, fmt.Errorf("restic: failed to start: %w", err)
	}

	res, found, parseErr := parseResticStats(stdout)
	if parseErr != nil {
		_ = cmd.Process.Kill()
		return StatsResult{}, fmt.Errorf("restic: error reading stats output: %w", parseErr)
	}

	if err := cmd.Wait(); err != nil {
		stderr := strings.TrimSpace(stderrBuf.String())
		return StatsResult{}, fmt.Errorf("restic: stats failed: %w\n%s", err, stderr)
	}
	if !found {
		return StatsResult{}, fmt.Errorf("restic: stats returned no parsable JSON output")
	}
	return res, nil
}

// parseResticStats extracts the repository size from `restic stats --json`
// output. restic interleaves a human-readable progress line with the JSON
// object on stdout, so only lines that parse as a JSON object are considered;
// the last such object wins. found is false when no JSON object was present.
func parseResticStats(r io.Reader) (StatsResult, bool, error) {
	var raw struct {
		TotalSize uint64 `json:"total_size"`
	}
	found := false
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		found = true
	}
	if err := scanner.Err(); err != nil {
		return StatsResult{}, false, err
	}
	return StatsResult{TotalSize: raw.TotalSize}, found, nil
}

// runRestoreJSON runs restic restore --json, consuming stdout as a JSON event
// stream (to prevent pipe stalls) and capturing stderr separately.
// On exit error, only stderr is inspected for lchown failures — mixing JSON
// stdout with text stderr via CombinedOutput would risk false-positive matches.
// lchown tolerance is applied only when hostRoot is non-empty (Docker deployments
// with a bind-mounted host filesystem); on native binaries hostRoot is "" and
// all errors are propagated unchanged.
func (w *Wrapper) runRestoreJSON(ctx context.Context, dest Destination, args []string, hostRoot string) error {
	return w.withLockRetry(ctx, dest, func() error {
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
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("restic: failed to read stdout: %w", err)
		}

		if err := cmd.Wait(); err != nil {
			stderr := strings.TrimSpace(stderrBuf.String())
			if hostRoot != "" && isOnlyHostRootLchownErrors(stderr, hostRoot) {
				return nil
			}
			return fmt.Errorf("restic: command failed: %w\n%s", err, stderr)
		}
		return nil
	})
}

// isOnlyHostRootLchownErrors returns true when every error restic encountered
// was an lchown/permission-denied failure on a directory that lives under
// hostRoot. This pattern occurs during in-place restores when the snapshot
// paths include host-OS mount point ancestors (e.g. /hostfs/c, /hostfs/c/Users)
// that the container cannot chown because the underlying filesystem (Windows
// NTFS) does not support Unix ownership. File data is intact in these cases.
// Errors on paths outside hostRoot are always treated as real failures.
//
// Handles both output formats:
//   - JSON (restic --json): {"message_type":"error","error":{"message":"lchown ..."},"item":"..."}
//   - Plain text (fallback): "ignoring error for /hostfs/c: lchown /hostfs/c: permission denied"
func isOnlyHostRootLchownErrors(stderr, hostRoot string) bool {
	foundLchownError := false
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// ── JSON format (restic --json emits errors to stderr as JSON objects) ──
		if strings.HasPrefix(line, "{") {
			var ev struct {
				MessageType string `json:"message_type"`
				Error       struct {
					Message string `json:"message"`
				} `json:"error"`
				Item string `json:"item"`
			}
			if err := json.Unmarshal([]byte(line), &ev); err == nil {
				switch ev.MessageType {
				case "error":
					isLchown := strings.Contains(ev.Error.Message, "lchown") &&
						strings.Contains(ev.Error.Message, "permission denied")
					isUnderHostRoot := strings.HasPrefix(ev.Item, hostRoot)
					if isLchown && isUnderHostRoot {
						foundLchownError = true
						continue
					}
					return false // non-lchown or outside hostRoot → real error
				case "exit_error":
					// {"message_type":"exit_error","code":1,"message":"Fatal: There were N errors\n"}
					// Acceptable summary when all individual errors are lchown.
					continue
				}
				continue // other JSON event types on stderr — skip
			}
		}

		// ── Plain-text format (fallback for restic without --json) ───────────────
		// "ignoring error for /hostfs/c: lchown /hostfs/c: permission denied"
		if strings.HasPrefix(line, "ignoring error for ") {
			isLchown := strings.Contains(line, "lchown") && strings.Contains(line, "permission denied")
			isUnderHostRoot := strings.Contains(line, hostRoot)
			if isLchown && isUnderHostRoot {
				foundLchownError = true
				continue
			}
			return false
		}
		if strings.HasPrefix(line, "Fatal: There were ") && strings.HasSuffix(line, "errors") {
			continue
		}
		if strings.HasPrefix(line, "Fatal:") || strings.HasPrefix(line, "Error:") {
			return false
		}
	}
	return foundLchownError
}

// lockConflictExitCode is restic's dedicated, stable exit status for "unable
// to create lock in backend: repository is already locked" — distinct from
// its other error codes, so detecting it doesn't require matching the
// human-readable (and version-dependent) error string.
const lockConflictExitCode = 11

// isLockConflict reports whether err is restic exiting because it could not
// acquire a repository lock. err may be wrapped (e.g. via fmt.Errorf's %w),
// so this unwraps down to the underlying *exec.ExitError rather than doing a
// direct type assertion.
func isLockConflict(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == lockConflictExitCode
}

// unlock runs `restic unlock` (without --remove-all) against dest's
// repository. This only removes locks restic itself judges stale — created
// more than 30 minutes ago, or created by a PID no longer running on this
// host — so it never touches a lock genuinely held by a live operation, even
// one running on a different host. Safe to call speculatively.
func (w *Wrapper) unlock(ctx context.Context, dest Destination) error {
	cmd := w.buildCmd(ctx, dest, []string{"unlock"})
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("restic: unlock failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// withLockRetry runs fn once. If fn fails because the repository is already
// locked, it attempts to clear a stale lock via unlock and, if that succeeds,
// runs fn again exactly once.
//
// This is what lets a backup recover from an exclusive lock orphaned by a
// forget/prune that was killed mid-run (see buildCmd's graceful-cancellation
// handling for the other half of this fix) instead of failing identically on
// every future run against the same destination. A second failure — including
// a fresh lock conflict, meaning some other operation is genuinely still
// running — is returned as-is; there is no unbounded retry loop.
func (w *Wrapper) withLockRetry(ctx context.Context, dest Destination, fn func() error) error {
	err := fn()
	if !isLockConflict(err) {
		return err
	}
	if unlockErr := w.unlock(ctx, dest); unlockErr != nil {
		// Could not confirm/clear the lock (e.g. it genuinely isn't stale) —
		// surface the original conflict rather than the unlock attempt's error.
		w.logger.Debug("restic: repository locked, unlock attempt did not clear it",
			zap.Error(unlockErr))
		return err
	}
	w.logger.Warn("restic: recovered from a stale repository lock, retrying")
	return fn()
}

// run executes a restic command and waits for it to finish.
// stderr is captured and included in the error if the command fails.
func (w *Wrapper) run(ctx context.Context, dest Destination, args []string) error {
	return w.withLockRetry(ctx, dest, func() error {
		cmd := w.buildCmd(ctx, dest, args)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("restic: command failed: %w\n%s", err, strings.TrimSpace(string(out)))
		}
		return nil
	})
}

// output executes a restic command and returns its stdout as raw bytes.
func (w *Wrapper) output(ctx context.Context, dest Destination, args []string) ([]byte, error) {
	var result []byte
	err := w.withLockRetry(ctx, dest, func() error {
		cmd := w.buildCmd(ctx, dest, args)
		out, err := cmd.Output()
		if err != nil {
			stderr := ""
			if ee, ok := err.(*exec.ExitError); ok {
				stderr = strings.TrimSpace(string(ee.Stderr))
			}
			return fmt.Errorf("restic: command failed: %w\n%s", err, stderr)
		}
		result = out
		return nil
	})
	return result, err
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
	return w.withLockRetry(ctx, dest, func() error {
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
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("restic: failed to read stdout: %w", err)
		}

		if err := cmd.Wait(); err != nil {
			stderr := strings.TrimSpace(stderrBuf.String())
			return fmt.Errorf("restic: command failed: %w\n%s", err, stderr)
		}
		return nil
	})
}

// buildCmd constructs the exec.Cmd for a restic invocation.
// It sets RESTIC_REPOSITORY, RESTIC_PASSWORD, and any backend-specific
// environment variables from dest.Env. For rclone destinations it also
// points restic at the embedded rclone binary via the "rclone.program" option.
func (w *Wrapper) buildCmd(ctx context.Context, dest Destination, args []string) *exec.Cmd {
	// For rclone-backed destinations, tell restic where the embedded rclone
	// binary lives. restic does NOT read any RCLONE_BINARY env var: the only
	// way to override the rclone path is the rclone backend's "program" option,
	// passed as a global "-o" flag before the subcommand. Without this, restic
	// looks for "rclone" on $PATH and fails on hosts that have no system rclone.
	// SFTP destinations are also routed through rclone, so key off the repo URL
	// prefix rather than the destination type.
	if strings.HasPrefix(dest.RepoURL, "rclone:") {
		args = append([]string{"-o", "rclone.program=" + w.rcloneBin}, args...)
	}

	cmd := exec.CommandContext(ctx, w.resticBin, args...)

	// By default, exec.CommandContext kills the process with SIGKILL the
	// instant ctx is cancelled — restic never gets a chance to run its own
	// shutdown handler, which releases its repository lock. Sending SIGINT
	// instead lets restic clean up (it explicitly handles this, same as a
	// user pressing Ctrl-C), avoiding an orphaned exclusive lock that would
	// otherwise block every future backup to this repository. WaitDelay is a
	// safety net: if restic doesn't exit within it, Go falls back to SIGKILL.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = cancelGracePeriod

	// Build environment: start from the current process env so that PATH,
	// HOME, and system variables are inherited, then overlay restic-specific
	// variables. This avoids having to enumerate every variable the OS needs.
	env := append(cmd.Environ(),
		"RESTIC_REPOSITORY="+dest.RepoURL,
		"RESTIC_PASSWORD="+dest.Password,
	)

	for k, v := range dest.Env {
		env = append(env, k+"="+v)
	}

	cmd.Env = env
	return cmd
}