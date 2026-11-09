package executor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/agent/internal/restic"
	"github.com/arkeep-io/arkeep/agent/internal/restic/restictest"
	"github.com/arkeep-io/arkeep/shared/proto"
)

// These tests run the real embedded restic binary through executeBackup and
// executeRestore, starting from the job payloads the server sends, for both
// deployments: native (no host root) and Docker on Linux (dockerHostRoot set,
// simulated with a temp directory).

// e2eSource is the host path every round trip backs up.
const e2eSource = "/home/u/data"

// e2eEnv is an agent plus a local repository, as seen from the server.
type e2eEnv struct {
	e        *Executor
	hostRoot string // dockerHostRoot of the agent; "" for a native agent
	repoURL  string // as the server sends it, host form

	// nativeRoot stands in for "/" on a native agent, whose paths are not
	// translated: host path p lives at nativeRoot+p, and that is also the
	// path the server sends. Empty with a host root, where the agent
	// translates p itself.
	nativeRoot string
}

// newE2EEnv returns an agent with an initialised repository at repoURL.
// hostRoot selects the Docker deployment (agent's host root is a temp dir).
func newE2EEnv(t *testing.T, hostRoot bool) *e2eEnv {
	t.Helper()
	env := &e2eEnv{}
	if hostRoot {
		env.hostRoot = t.TempDir() + "/hostfs"
		env.repoURL = "/backups/repo"
	} else {
		env.nativeRoot = t.TempDir()
		env.repoURL = t.TempDir() + "/repo"
	}
	env.e = New(restictest.Wrapper(t), nil, nil, zap.NewNop(), env.hostRoot, "e2e-host")
	// Docker volume handling is not under test here: point it at a path
	// that does not exist so the host's real /var/lib/docker/volumes never
	// changes the outcome. Tests about it set their own.
	env.e.dockerVolRoot = filepath.Join(t.TempDir(), "no-docker-volumes")
	// Not the container the tests may themselves run in.
	env.e.selfContainerID = ""
	restictest.CopyInitializedRepo(t, env.repoPath())
	return env
}

// path returns host path p as the server sends it in a payload.
func (env *e2eEnv) path(p string) string {
	return env.nativeRoot + p
}

// hostPath returns where host path p lives on disk in the test.
func (env *e2eEnv) hostPath(p string) string {
	return translateLocalPath(env.path(p), env.hostRoot)
}

// repoPath returns where the repository lives on disk in the test.
func (env *e2eEnv) repoPath() string {
	return translateLocalPath(env.repoURL, env.hostRoot)
}

// dest returns the repository as a restic destination, for direct wrapper calls.
func (env *e2eEnv) dest() restic.Destination {
	return restic.Destination{Type: restic.DestLocal, RepoURL: env.repoPath(), Password: "test-password"}
}

// backup runs a backup job of the given host paths and returns the snapshot ID.
func (env *e2eEnv) backup(t *testing.T, sources ...string) string {
	t.Helper()
	paths := make([]string, len(sources))
	for i, s := range sources {
		if strings.HasPrefix(s, "docker-volume://") {
			paths[i] = s // not a path: the agent resolves it through Docker
			continue
		}
		paths[i] = env.path(s)
	}
	srcJSON, err := json.Marshal(paths)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(backupPayload{
		Sources:      string(srcJSON),
		RepoPassword: "test-password",
		Destinations: []destinationPayload{{DestinationID: "dest-1", Type: "local", RepoURL: env.repoURL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordingSink{}
	reporter := &fakeReporter{}
	env.e.executeBackup(context.Background(), JobAssignment{JobID: "backup-1", Type: proto.JobType_JOB_TYPE_BACKUP, Payload: raw}, sink, reporter)
	if got := lastStatus(reporter); got != "succeeded" {
		t.Fatalf("backup status = %q (%s), want succeeded; logs:\n%v", got, reporter.lastMsg, sink.logs)
	}
	id := reporter.snapshotIDs["dest-1"]
	if id == "" {
		t.Fatal("backup reported no snapshot id for dest-1")
	}
	return id
}

// restore runs a restore job. Destination is filled from env, and
// RepoPassword too when empty. TargetPath is sent as given; use env.path for
// any target other than the in-place sentinel "/". sink may be nil.
func (env *e2eEnv) restore(t *testing.T, ctx context.Context, p restorePayload, sink *recordingSink) *fakeReporter {
	t.Helper()
	p.Destination = destinationPayload{DestinationID: "dest-1", Type: "local", RepoURL: env.repoURL}
	if p.RepoPassword == "" {
		p.RepoPassword = "test-password"
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if sink == nil {
		sink = &recordingSink{}
	}
	reporter := &fakeReporter{}
	env.e.executeRestore(ctx, JobAssignment{JobID: "restore-1", Type: proto.JobType_JOB_TYPE_RESTORE, Payload: raw}, sink, reporter)
	return reporter
}

func lastStatus(r *fakeReporter) string {
	if len(r.statuses) == 0 {
		return ""
	}
	return r.statuses[len(r.statuses)-1]
}

// mustSucceed fails the test unless the restore job ended as succeeded.
func mustSucceed(t *testing.T, r *fakeReporter, sink *recordingSink) {
	t.Helper()
	if got := lastStatus(r); got != "succeeded" {
		t.Fatalf("restore status = %q (%s), want succeeded; logs:\n%v", got, r.lastMsg, sink.logs)
	}
}

// forEachDeployment runs fn as parallel subtests "native" and "docker". It
// also marks t parallel, so call it from the top of a test function.
func forEachDeployment(t *testing.T, fn func(t *testing.T, env *e2eEnv)) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		hostRoot bool
	}{{"native", false}, {"docker", true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fn(t, newE2EEnv(t, tc.hostRoot))
		})
	}
}

// writeSource writes the fixture at the source host path and returns it.
func (env *e2eEnv) writeSource(t *testing.T) restictest.Tree {
	t.Helper()
	restictest.WriteTree(t, env.hostPath(e2eSource))
	return restictest.Capture(t, env.hostPath(e2eSource))
}

// restoredUnder returns where a to-folder restore into host path target puts
// the source: restic recreates the snapshot's absolute path (the on-disk
// source path, i.e. including the host root) below the target.
func (env *e2eEnv) restoredUnder(target string) string {
	return env.hostPath(target) + env.hostPath(e2eSource)
}

func TestRestoreE2E_ToFolder(t *testing.T) {
	forEachDeployment(t, func(t *testing.T, env *e2eEnv) {
		want := env.writeSource(t)
		id := env.backup(t, e2eSource)

		sink := &recordingSink{}
		r := env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: env.path("/home/u/restore")}, sink)
		mustSucceed(t, r, sink)
		restictest.AssertTreeMatches(t, want, env.restoredUnder("/home/u/restore"))
		// The original is untouched by a restore elsewhere.
		restictest.AssertTreeMatches(t, want, env.hostPath(e2eSource))
	})
}

func TestRestoreE2E_ToFolder_TargetMissingAndExisting(t *testing.T) {
	forEachDeployment(t, func(t *testing.T, env *e2eEnv) {
		want := env.writeSource(t)
		id := env.backup(t, e2eSource)

		t.Run("missing", func(t *testing.T) {
			target := "/home/u/new/restore"
			if _, err := os.Stat(env.hostPath(target)); !os.IsNotExist(err) {
				t.Fatalf("target exists before restore: %v", err)
			}
			sink := &recordingSink{}
			r := env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: env.path(target)}, sink)
			mustSucceed(t, r, sink)
			restictest.AssertTreeMatches(t, want, env.restoredUnder(target))
		})

		t.Run("existing", func(t *testing.T) {
			target := "/home/u/existing"
			keep := filepath.Join(env.hostPath(target), "keep.txt")
			if err := os.MkdirAll(filepath.Dir(keep), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(keep, []byte("keep\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			sink := &recordingSink{}
			r := env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: env.path(target)}, sink)
			mustSucceed(t, r, sink)
			restictest.AssertTreeMatches(t, want, env.restoredUnder(target))
			// restic never deletes files it did not restore.
			if data, err := os.ReadFile(keep); err != nil || string(data) != "keep\n" {
				t.Errorf("keep.txt after restore = %q, %v; want it untouched", data, err)
			}
		})
	})
}

func TestRestoreE2E_InPlace(t *testing.T) {
	forEachDeployment(t, func(t *testing.T, env *e2eEnv) {
		want := env.writeSource(t)
		id := env.backup(t, e2eSource)
		restictest.DamageTree(t, env.hostPath(e2eSource))

		sink := &recordingSink{}
		r := env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: "/"}, sink)
		mustSucceed(t, r, sink)
		// restic does not delete files absent from the snapshot.
		restictest.AssertTreeMatches(t, want, env.hostPath(e2eSource), "extra.txt")
	})
}

func TestRestoreE2E_InPlace_Include(t *testing.T) {
	forEachDeployment(t, func(t *testing.T, env *e2eEnv) {
		env.writeSource(t)
		id := env.backup(t, e2eSource)

		// The GUI sends include paths as listed by the snapshot browser.
		src := env.hostPath(e2eSource)
		entries, err := env.e.wrapper.Ls(context.Background(), env.dest(), id, src)
		if err != nil {
			t.Fatalf("ls: %v", err)
		}
		var include string
		for _, e := range entries {
			if filepath.Base(e.Path) == "dir with space" {
				include = e.Path
			}
		}
		if include == "" {
			t.Fatalf("ls did not return %q: %+v", "dir with space", entries)
		}

		aTxt := filepath.Join(src, "a.txt")
		unicode := filepath.Join(src, "dir with space", "ünïcödé.txt")
		for _, p := range []string{aTxt, unicode} {
			if err := os.WriteFile(p, []byte("damaged\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		sink := &recordingSink{}
		r := env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: "/", IncludePaths: []string{include}}, sink)
		mustSucceed(t, r, sink)

		if data, _ := os.ReadFile(unicode); string(data) != "unicode\n" {
			t.Errorf("included file after restore = %q, want restored", data)
		}
		if data, _ := os.ReadFile(aTxt); string(data) != "damaged\n" {
			t.Errorf("a.txt (not included) after restore = %q, want it left damaged", data)
		}
	})
}

func TestRestoreE2E_Latest(t *testing.T) {
	forEachDeployment(t, func(t *testing.T, env *e2eEnv) {
		env.writeSource(t)
		env.backup(t, e2eSource)

		// A second snapshot with different content: "latest" must pick it,
		// not the first one.
		aTxt := filepath.Join(env.hostPath(e2eSource), "a.txt")
		if err := os.WriteFile(aTxt, []byte("second snapshot\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		want := restictest.Capture(t, env.hostPath(e2eSource))
		env.backup(t, e2eSource)
		restictest.DamageTree(t, env.hostPath(e2eSource))

		sink := &recordingSink{}
		r := env.restore(t, context.Background(), restorePayload{ResticSnapshotID: "latest", TargetPath: "/"}, sink)
		mustSucceed(t, r, sink)
		restictest.AssertTreeMatches(t, want, env.hostPath(e2eSource), "extra.txt")
		if data, _ := os.ReadFile(aTxt); string(data) != "second snapshot\n" {
			t.Errorf("a.txt after restoring latest = %q, want the newer content", data)
		}
	})
}

// TestRestoreE2E_BothModesSameSnapshot is the maintainer's worst case: both
// restore modes must work on the same snapshot. It starts from a repository
// that does not exist, so the backup's auto-init path is covered too.
func TestRestoreE2E_BothModesSameSnapshot(t *testing.T) {
	forEachDeployment(t, func(t *testing.T, env *e2eEnv) {
		if err := os.RemoveAll(env.repoPath()); err != nil {
			t.Fatal(err)
		}
		want := env.writeSource(t)
		id := env.backup(t, e2eSource)

		restictest.DamageTree(t, env.hostPath(e2eSource))
		sink := &recordingSink{}
		r := env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: "/"}, sink)
		mustSucceed(t, r, sink)
		restictest.AssertTreeMatches(t, want, env.hostPath(e2eSource), "extra.txt")

		sink = &recordingSink{}
		r = env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: env.path("/home/u/restore")}, sink)
		mustSucceed(t, r, sink)
		restictest.AssertTreeMatches(t, want, env.restoredUnder("/home/u/restore"))
	})
}
