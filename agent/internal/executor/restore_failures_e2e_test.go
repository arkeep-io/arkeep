package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests run real restic restores that must fail, and assert the job
// ends in the right final status without ever reporting success.

// mustEndAs fails the test unless the final status is want and "succeeded"
// was never reported.
func mustEndAs(t *testing.T, r *fakeReporter, sink *recordingSink, want string) {
	t.Helper()
	for _, s := range r.statuses {
		if s == "succeeded" {
			t.Fatalf("restore reported succeeded (statuses %v), want %s; logs:\n%v", r.statuses, want, sink.logs)
		}
	}
	if got := lastStatus(r); got != want {
		t.Fatalf("restore status = %q (%s), want %s; logs:\n%v", got, r.lastMsg, want, sink.logs)
	}
}

// mustFailInRestic asserts the job failed because the restic run itself
// failed (not payload validation or any earlier step): restore started, and
// the final message is the restic-run failure. The cause is pinned through
// restic's exit code, its stable contract, when a specific one exists (12
// wrong password, 10 repository missing); pass "" for the generic exit status
// 1. The message also carries restic's own error text (the exit_error message
// of its --json output, not the raw JSON), asserted only where a test needs
// it, since its wording can change between restic versions.
func mustFailInRestic(t *testing.T, r *fakeReporter, sink *recordingSink, exitStatus string) {
	t.Helper()
	mustEndAs(t, r, sink, "failed")
	if !sink.contains("restore started") {
		t.Errorf("restore never started, so the failure is not from the restic run; logs:\n%v", sink.logs)
	}
	if !strings.HasPrefix(r.lastMsg, "restore failed:") {
		t.Errorf("last message = %q, want prefix %q", r.lastMsg, "restore failed:")
	}
	if exitStatus != "" && !strings.Contains(r.lastMsg, "exit status "+exitStatus) {
		t.Errorf("last message = %q, want it to contain %q", r.lastMsg, "exit status "+exitStatus)
	}
}

// failureEnv returns a Docker-deployment env with a backed-up source and the
// snapshot ID.
func failureEnv(t *testing.T) (*e2eEnv, string) {
	t.Helper()
	env := newE2EEnv(t, true)
	env.writeSource(t)
	return env, env.backup(t, e2eSource)
}

func TestRestoreE2E_WrongPassword(t *testing.T) {
	t.Parallel()
	env, id := failureEnv(t)
	sink := &recordingSink{}
	r := env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: env.path("/home/u/restore"), RepoPassword: "wrong-password"}, sink)
	mustFailInRestic(t, r, sink, "12")
	// The user sees restic's message, not its raw JSON output.
	if !strings.Contains(r.lastMsg, "wrong password") || strings.Contains(r.lastMsg, "message_type") {
		t.Errorf("last message = %q, want restic's readable \"wrong password\" message without raw JSON", r.lastMsg)
	}
}

func TestRestoreE2E_MissingRepository(t *testing.T) {
	t.Parallel()
	env, id := failureEnv(t)
	if err := os.RemoveAll(env.repoPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(env.repoPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	sink := &recordingSink{}
	r := env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: env.path("/home/u/restore")}, sink)
	mustFailInRestic(t, r, sink, "10")
}

func TestRestoreE2E_CorruptedRepository(t *testing.T) {
	t.Parallel()
	env, id := failureEnv(t)
	dataDir := filepath.Join(env.repoPath(), "data")
	entries, err := os.ReadDir(dataDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("repository data dir %s: %d entries, %v", dataDir, len(entries), err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dataDir, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	sink := &recordingSink{}
	r := env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: env.path("/home/u/restore")}, sink)
	mustFailInRestic(t, r, sink, "")
}

func TestRestoreE2E_UnknownSnapshotID(t *testing.T) {
	t.Parallel()
	env, _ := failureEnv(t)
	sink := &recordingSink{}
	r := env.restore(t, context.Background(), restorePayload{ResticSnapshotID: "deadbeef", TargetPath: env.path("/home/u/restore")}, sink)
	mustFailInRestic(t, r, sink, "")
}

func TestRestoreE2E_IncludeMatchesNothing(t *testing.T) {
	t.Parallel()
	env, id := failureEnv(t)
	sink := &recordingSink{}
	r := env.restore(t, context.Background(), restorePayload{
		ResticSnapshotID: id,
		TargetPath:       env.path("/home/u/restore"),
		IncludePaths:     []string{env.hostPath(e2eSource) + "/does-not-exist"},
	}, sink)
	mustEndAs(t, r, sink, "failed")
	if !strings.Contains(r.lastMsg, "nothing in the snapshot matched") {
		t.Errorf("last message = %q, want it to contain %q", r.lastMsg, "nothing in the snapshot matched")
	}
}

// Non-lchown errors under the host root must not be swallowed.
func TestRestoreE2E_UnwritableTarget(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory mode bits, so the target cannot be made unwritable")
	}
	env, id := failureEnv(t)
	parent := env.hostPath("/home/u/readonly")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	sink := &recordingSink{}
	r := env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: env.path("/home/u/readonly/restore")}, sink)
	mustEndAs(t, r, sink, "failed")
}

// The cancel fires on the "restore started" log, which executeRestore emits
// just before wrapper.Restore, so restic is started with an already cancelled
// context. This covers the ctx.Err() -> "cancelled" branch (a cancel is never
// reported as failed), not killing a running restic: signal handling of a
// running restic is covered by TestBuildCmd_GracefulCancelSendsSIGINT in the
// restic package.
func TestRestoreE2E_CancelledMidRestore(t *testing.T) {
	t.Parallel()
	env, id := failureEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := &recordingSink{onLog: func(_, msg string) {
		if strings.Contains(msg, "restore started") {
			cancel()
		}
	}}
	r := env.restore(t, ctx, restorePayload{ResticSnapshotID: id, TargetPath: env.path("/home/u/restore")}, sink)
	mustEndAs(t, r, sink, "cancelled")
}
