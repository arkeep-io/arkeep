// Package hooks handles the execution of user-defined shell commands that run
// before and after a backup job. Hooks are configured per-policy in the
// HookPreBackup and HookPostBackup fields (see server/internal/db/models.go).
//
// Hooks run as blocking subprocesses with a configurable timeout. stdout and
// stderr are captured and returned to the caller so they can be included in
// the job log stream. A non-zero exit code causes the hook to be considered
// failed — for pre-backup hooks this aborts the job; for post-backup hooks
// the failure is logged but does not change the job outcome.
//
// The shell used depends on the host OS:
//   - Linux / macOS: /bin/sh -c "<command>"
//   - Windows:       cmd /C "<command>"
package hooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

// DefaultTimeout is applied when the caller does not specify a timeout.
// 5 minutes is generous for typical pre/post scripts (pg_dump, mysqldump, etc.)
// while still preventing a stalled hook from blocking the job indefinitely.
const DefaultTimeout = 5 * time.Minute

// ErrHookFailed is returned when the hook process exits with a non-zero code.
// It wraps the exit error so callers can inspect it with errors.As if needed.
var ErrHookFailed = errors.New("hook: command failed")

// Result holds the outcome of a single hook execution.
type Result struct {
	// Output is the combined stdout+stderr of the hook process, trimmed of
	// leading/trailing whitespace. Included verbatim in the job log stream.
	Output   string
	// ExitCode is the exit code of the hook process. 0 means success.
	ExitCode int
	// Duration is how long the hook took to run.
	Duration time.Duration
}

// Runner executes pre/post backup hooks.
// The zero value is usable — create with NewRunner or use directly.
type Runner struct {
	// Timeout overrides DefaultTimeout when non-zero.
	Timeout time.Duration
}

// NewRunner creates a Runner with the given timeout.
// Pass 0 to use DefaultTimeout.
func NewRunner(timeout time.Duration) *Runner {
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	return &Runner{Timeout: timeout}
}

// Run executes the given shell command string and returns its result.
// The command is run inside a shell (sh or cmd) so pipes, redirects,
// and shell builtins work as expected.
//
// If the parent context is cancelled before the timeout, the subprocess
// is killed immediately and ctx.Err() is returned.
//
// A non-zero exit code returns ErrHookFailed wrapping the underlying
// exec.ExitError — the Result is still populated so the caller can log
// the output regardless.
func (r *Runner) Run(ctx context.Context, command string) (*Result, error) {
	if command == "" {
		// No hook configured — treat as success with no output.
		return &Result{}, nil
	}

	// Apply the runner timeout on top of any deadline already in ctx.
	// context.WithTimeout returns the shorter of the two deadlines.
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	cmd := buildShellCmd(ctx, command)

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	output := buf.String()

	if err != nil {
		exitCode := 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}

		// Context cancellation takes priority in the error message so the
		// caller knows whether it was a timeout or a genuine script failure.
		if ctx.Err() != nil {
			return &Result{
				Output:   output,
				ExitCode: exitCode,
				Duration: duration,
			}, fmt.Errorf("%w: %w", ErrHookFailed, ctx.Err())
		}

		return &Result{
			Output:   output,
			ExitCode: exitCode,
			Duration: duration,
		}, fmt.Errorf("%w: exit code %d", ErrHookFailed, exitCode)
	}

	return &Result{
		Output:   output,
		ExitCode: 0,
		Duration: duration,
	}, nil
}

// buildShellCmd constructs the exec.Cmd that wraps the command string in the
// appropriate shell for the current OS.
//
// Using a shell (rather than splitting the command string manually) means
// hooks can use pipes, environment variable expansion, conditionals, and
// other shell features — consistent with what users expect from a "shell
// command" field in the GUI.
func buildShellCmd(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "/bin/sh", "-c", command)
}