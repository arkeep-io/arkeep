//go:build !windows

package hooks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// childPID reads the PID a hook wrote to file.
func childPID(t *testing.T, file string) int {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse child pid %q: %v", data, err)
	}
	return pid
}

// alive reports whether pid is still running, waiting up to a second for a
// killed process to be reaped.
func alive(pid int) bool {
	for range 20 {
		if syscall.Kill(pid, 0) != nil {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	return true
}

// TestRun_TimeoutKillsTheWholeHook covers a hook whose child outlives the
// shell: on timeout the child is killed too, and Run returns at once instead
// of waiting for it.
func TestRun_TimeoutKillsTheWholeHook(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	r := NewRunner(0)

	start := time.Now()
	_, err := r.Run(context.Background(), "sleep 30 & echo $! > "+pidFile+"; wait", nil, time.Second)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrHookFailed) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Run error = %v, want a timed-out hook", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("Run returned after %v, want about the 1s timeout", elapsed)
	}
	if pid := childPID(t, pidFile); alive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Errorf("child %d still running after the hook timed out", pid)
	}
}

// TestRun_BackgroundChildDoesNotHoldTheHook covers a hook that succeeds and
// leaves a process in the background (a service it starts): Run returns
// shortly after the hook exits, reports success, and leaves the process be.
func TestRun_BackgroundChildDoesNotHoldTheHook(t *testing.T) {
	old := outputWaitDelay
	outputWaitDelay = 200 * time.Millisecond
	t.Cleanup(func() { outputWaitDelay = old })

	pidFile := filepath.Join(t.TempDir(), "pid")
	r := NewRunner(0)

	start := time.Now()
	res, err := r.Run(context.Background(), "echo started; sleep 30 & echo $! > "+pidFile, nil, 10*time.Second)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Run error = %v, want success", err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Output, "started") {
		t.Errorf("result = %+v, want exit 0 and the hook's output", res)
	}
	if elapsed > 3*time.Second {
		t.Errorf("Run returned after %v, want shortly after the hook exited", elapsed)
	}
	pid := childPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if syscall.Kill(pid, 0) != nil {
		t.Errorf("background child %d was killed, want it left running", pid)
	}
}

// TestRun_ExitCodeAndOutput keeps the plain cases: output and a non-zero exit.
func TestRun_ExitCodeAndOutput(t *testing.T) {
	r := NewRunner(0)
	res, err := r.Run(context.Background(), `echo "$1"; exit 3`, []string{"hello"}, 5*time.Second)
	if !errors.Is(err, ErrHookFailed) || res.ExitCode != 3 || res.Output != "hello\n" {
		t.Errorf("Run = %+v, %v; want exit 3 with output %q", res, err, "hello\n")
	}
}
