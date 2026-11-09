package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/agent/internal/docker"
	"github.com/arkeep-io/arkeep/agent/internal/hooks"
	"github.com/arkeep-io/arkeep/agent/internal/restic"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

type fakeSink struct{}

func (fakeSink) SendLog(jobID, level, message string) {}

// recordingSink is a LogSink that keeps every message and optionally calls
// onLog for each one.
type recordingSink struct {
	mu    sync.Mutex
	logs  []string
	onLog func(level, msg string)
}

func (s *recordingSink) SendLog(jobID, level, message string) {
	s.mu.Lock()
	s.logs = append(s.logs, message)
	s.mu.Unlock()
	if s.onLog != nil {
		s.onLog(level, message)
	}
}

func (s *recordingSink) contains(substr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.logs {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}

type destResult struct {
	status string
	errMsg string
}

type commandResult struct {
	destinationID string
	sourceName    string
	status        string
	errMsg        string
}

type retentionTagResult struct {
	destinationID string
	tag           string
	status        string
	errMsg        string
}

type fakeReporter struct {
	statuses         []string
	lastMsg          string
	destResults      map[string]destResult
	commandResults   []commandResult
	retentionResults []retentionTagResult
	snapshotIDs      map[string]string
}

func (r *fakeReporter) ReportStatus(jobID, status, message string) {
	r.statuses = append(r.statuses, status)
	r.lastMsg = message
}

func (r *fakeReporter) ReportDestinationResult(jobID, destinationID, status, snapshotID string, startedAt time.Time, sizeBytes, repoSizeBytes int64, errMsg string) {
	if r.destResults == nil {
		r.destResults = make(map[string]destResult)
	}
	r.destResults[destinationID] = destResult{status: status, errMsg: errMsg}
	if r.snapshotIDs == nil {
		r.snapshotIDs = make(map[string]string)
	}
	r.snapshotIDs[destinationID] = snapshotID
}

func (r *fakeReporter) ReportCommandSourceResult(jobID, destinationID, sourceName, status, snapshotID string, startedAt time.Time, sizeBytes int64, errMsg string) {
	r.commandResults = append(r.commandResults, commandResult{
		destinationID: destinationID,
		sourceName:    sourceName,
		status:        status,
		errMsg:        errMsg,
	})
}

func (r *fakeReporter) ReportRetentionTagResult(jobID, destinationID, tag, status string, startedAt time.Time, errMsg string) {
	r.retentionResults = append(r.retentionResults, retentionTagResult{
		destinationID: destinationID,
		tag:           tag,
		status:        status,
		errMsg:        errMsg,
	})
}

func (r *fakeReporter) ReportSnapshotReconcile(jobID, destinationID string, liveIDs []string, listedAt time.Time, repoSizeBytes int64) int64 {
	return 0
}

// TestExecuteBackupEmptyRepoURL verifies that a destination with an empty
// repo_url is reported as failed and causes the whole job to fail, rather than
// being silently skipped while the job is reported as succeeded.
func TestExecuteBackupEmptyRepoURL(t *testing.T) {
	payload := backupPayload{
		Sources:      `["/tmp"]`,
		RepoPassword: "pw",
		Destinations: []destinationPayload{
			{DestinationID: "dest-1", Type: "sftp", RepoURL: ""},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	e := New(nil, nil, nil, zap.NewNop(), "", "")
	reporter := &fakeReporter{}
	job := JobAssignment{JobID: "job-1", Type: proto.JobType_JOB_TYPE_BACKUP, Payload: raw}

	e.executeBackup(context.Background(), job, fakeSink{}, reporter)

	if got := reporter.destResults["dest-1"].status; got != "failed" {
		t.Errorf("destination status = %q, want %q", got, "failed")
	}
	if len(reporter.statuses) == 0 {
		t.Fatal("no job status reported")
	}
	if final := reporter.statuses[len(reporter.statuses)-1]; final != "failed" {
		t.Errorf("final job status = %q, want %q (all statuses: %v)", final, "failed", reporter.statuses)
	}
}

// TestExecuteRetention_MalformedPayloadFails verifies that a retention job
// (JOB_TYPE_FORGET) with an undeserializable payload is reported as failed,
// mirroring TestExecuteBackupEmptyRepoURL's shape for the backup path. This
// is the deepest executeRetention path testable without a real restic
// wrapper (unlike backup, retention has no destination/repo_url validation
// gate before its first wrapper call — every other path touches e.wrapper).
func TestExecuteRetention_MalformedPayloadFails(t *testing.T) {
	e := New(nil, nil, nil, zap.NewNop(), "", "")
	reporter := &fakeReporter{}
	job := JobAssignment{JobID: "job-1", Type: proto.JobType_JOB_TYPE_FORGET, Payload: []byte("not json")}

	e.executeRetention(context.Background(), job, fakeSink{}, reporter)

	if len(reporter.statuses) == 0 {
		t.Fatal("no job status reported")
	}
	if final := reporter.statuses[len(reporter.statuses)-1]; final != "failed" {
		t.Errorf("final job status = %q, want %q (all statuses: %v)", final, "failed", reporter.statuses)
	}
}

// TestExecuteCheck_FailsBeforeRunningRestic covers the executeCheck paths that
// end before restic runs: an undeserializable payload, and a check mode the
// wrapper refuses. The latter must still report a failed destination result,
// which is what releases the destination's busy gate on the server.
func TestExecuteCheck_FailsBeforeRunningRestic(t *testing.T) {
	t.Run("malformed payload", func(t *testing.T) {
		e := New(nil, nil, nil, zap.NewNop(), "", "")
		reporter := &fakeReporter{}
		job := JobAssignment{JobID: "job-1", Type: proto.JobType_JOB_TYPE_VERIFY, Payload: []byte("not json")}

		e.executeCheck(context.Background(), job, fakeSink{}, reporter)

		if !slices.Equal(reporter.statuses, []string{"failed"}) {
			t.Errorf("statuses = %v, want [failed]", reporter.statuses)
		}
	})

	t.Run("unknown mode", func(t *testing.T) {
		e := New(nil, nil, nil, zap.NewNop(), "", "")
		reporter := &fakeReporter{}
		payload, _ := json.Marshal(checkPayload{
			Destination: destinationPayload{DestinationID: "dest-1", Type: "local", RepoURL: "/tmp/repo"},
			Mode:        "deep",
		})
		job := JobAssignment{JobID: "job-1", Type: proto.JobType_JOB_TYPE_VERIFY, Payload: payload}

		e.executeCheck(context.Background(), job, fakeSink{}, reporter)

		if !slices.Equal(reporter.statuses, []string{"running", "failed"}) {
			t.Errorf("statuses = %v, want [running failed]", reporter.statuses)
		}
		if got := reporter.destResults["dest-1"]; got.status != "failed" || got.errMsg == "" {
			t.Errorf("destination result = %+v, want failed with an error", got)
		}
	})
}

// TestResolveSources_RejectsFlagLikeEntries is a regression test for
// GHSA-263g-c333-jcjq / GHSA-75rg-4ppf-pq7g: resolveSources is the agent-side
// defense-in-depth gate that must still reject a flag-like source even if a
// malicious policy predates server-side validation.
func TestResolveSources_RejectsFlagLikeEntries(t *testing.T) {
	e := New(nil, nil, nil, zap.NewNop(), "", "")
	noopLog := func(level, msg string) {}

	_, _, err := e.resolveSources(context.Background(), `["--password-command=touch /tmp/pwned"]`, noopLog)
	if err == nil {
		t.Fatal("expected an error rejecting a flag-like source, got nil")
	}
}

// TestResolveSources_AcceptsNormalPaths guards against over-restricting: the
// flag-like check must not reject legitimate source paths.
func TestResolveSources_AcceptsNormalPaths(t *testing.T) {
	e := New(nil, nil, nil, zap.NewNop(), "", "")
	noopLog := func(level, msg string) {}

	got, _, err := e.resolveSources(context.Background(), `["/data", "C:\\Users"]`, noopLog)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"/data", `C:\Users`}
	if !slices.Equal(got, want) {
		t.Errorf("resolveSources() = %v, want %v", got, want)
	}
}

// TestExecuteBackup_CommandOnlyPolicyDoesNotFailOnEmptySources verifies that
// a policy with zero regular sources but at least one command source is not
// rejected by the "no accessible backup sources" guard — that guard must
// only fire when there are neither regular nor command sources at all.
func TestExecuteBackup_CommandOnlyPolicyDoesNotFailOnEmptySources(t *testing.T) {
	payload := backupPayload{
		Sources:      `[]`,
		RepoPassword: "pw",
		Destinations: []destinationPayload{
			{DestinationID: "dest-1", Type: "sftp", RepoURL: ""},
		},
		CommandSources: []commandSourcePayload{
			{Name: "pgdump", Command: "pg_dump mydb", Tags: []string{"policy:abc:command:pgdump"}},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	e := New(nil, nil, nil, zap.NewNop(), "", "")
	reporter := &fakeReporter{}
	job := JobAssignment{JobID: "job-1", Type: proto.JobType_JOB_TYPE_BACKUP, Payload: raw}

	e.executeBackup(context.Background(), job, fakeSink{}, reporter)

	// The empty-sources guard reports failure via fail() before ever reaching
	// the destination loop, so no destResults entry would exist if it had
	// fired. Reaching (and failing on) the empty repo_url instead proves we
	// got past the guard.
	if _, ok := reporter.destResults["dest-1"]; !ok {
		t.Fatal("expected a destination result to be recorded — the empty-sources guard should not have fired")
	}
	if got := reporter.destResults["dest-1"].status; got != "failed" {
		t.Errorf("destination status = %q, want %q (empty repo_url)", got, "failed")
	}
}

// TestExecuteBackup_FailedDestinationSkipsPostBackupCommands verifies that
// once a destination's backup has failed, the executor does not go on to run
// `restic stats` / `restic snapshots` against the same repository. Against an
// unreachable repository those commands sit in restic's backend retry loop
// for minutes, keeping the job "running" long after the destination failed.
func TestExecuteBackup_FailedDestinationSkipsPostBackupCommands(t *testing.T) {
	tests := []struct {
		name           string
		sources        string
		commandSources []commandSourcePayload
	}{
		{name: "regular sources", sources: `["/tmp"]`},
		{
			name:           "command sources only",
			sources:        `[]`,
			commandSources: []commandSourcePayload{{Name: "pgdump", Command: "pg_dump mydb"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			callLog := filepath.Join(dir, "calls")
			script := filepath.Join(dir, "fake-restic.sh")
			// Records each subcommand and fails every one of them, like a
			// repository restic cannot reach.
			body := "#!/bin/sh\necho \"$1\" >> '" + callLog + "'\necho 'Fatal: repository unreachable' >&2\nexit 1\n"
			if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}

			payload := backupPayload{
				Sources:        tt.sources,
				RepoPassword:   "pw",
				Destinations:   []destinationPayload{{DestinationID: "dest-1", Type: "s3", RepoURL: "s3:s3.example.com/b/"}},
				CommandSources: tt.commandSources,
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}

			w := restic.NewWrapperWithBinaries(script, "/fake/rclone", zap.NewNop())
			e := New(w, nil, nil, zap.NewNop(), "", "")
			reporter := &fakeReporter{}
			job := JobAssignment{JobID: "job-1", Type: proto.JobType_JOB_TYPE_BACKUP, Payload: raw}

			e.executeBackup(context.Background(), job, fakeSink{}, reporter)

			if got := reporter.destResults["dest-1"].status; got != "failed" {
				t.Errorf("destination status = %q, want %q", got, "failed")
			}
			if final := reporter.statuses[len(reporter.statuses)-1]; final != "failed" {
				t.Errorf("final job status = %q, want %q (all statuses: %v)", final, "failed", reporter.statuses)
			}
			data, err := os.ReadFile(callLog)
			if err != nil {
				t.Fatal(err)
			}
			for _, sub := range strings.Fields(string(data)) {
				if sub == "stats" || sub == "snapshots" {
					t.Errorf("restic %s ran against a destination whose backup failed (calls: %q)", sub, data)
				}
			}
		})
	}
}

func TestTranslateLocalPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		hostRoot string
		want     string
	}{
		{
			name:     "unix path with hostRoot",
			path:     "/home/user/backups",
			hostRoot: "/hostfs",
			want:     "/hostfs/home/user/backups",
		},
		{
			name:     "already under hostRoot — no double-prefix",
			path:     "/hostfs/home/user/backups",
			hostRoot: "/hostfs",
			want:     "/hostfs/home/user/backups",
		},
		{
			name:     "windows backslash path",
			path:     `C:\Users\Filippo\data`,
			hostRoot: "/hostfs",
			want:     "/hostfs/c/Users/Filippo/data",
		},
		{
			name:     "windows forward-slash path",
			path:     "C:/Users/Filippo/data",
			hostRoot: "/hostfs",
			want:     "/hostfs/c/Users/Filippo/data",
		},
		{
			name:     "empty hostRoot — path unchanged",
			path:     "/some/path",
			hostRoot: "",
			want:     "/some/path",
		},
		{
			name:     "relative path — unchanged",
			path:     "relative/path",
			hostRoot: "/hostfs",
			want:     "relative/path",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translateLocalPath(tt.path, tt.hostRoot)
			if got != tt.want {
				t.Errorf("translateLocalPath(%q, %q) = %q, want %q", tt.path, tt.hostRoot, got, tt.want)
			}
		})
	}
}

func TestEnsureWritableDir(t *testing.T) {
	t.Run("existing leaf with read-only intermediate (Unraid scenario)", func(t *testing.T) {
		base := t.TempDir()
		mnt := filepath.Join(base, "mnt")
		leaf := filepath.Join(mnt, "user", "Arkeep")
		if err := os.MkdirAll(leaf, 0755); err != nil {
			t.Fatal(err)
		}
		// Make the intermediate read-only, like Unraid's /mnt overlay.
		if err := os.Chmod(mnt, 0555); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(mnt, 0755) //nolint:errcheck

		if err := ensureWritableDir(leaf); err != nil {
			t.Errorf("expected nil, got: %v", err)
		}
	})

	t.Run("non-existing leaf with read-only intermediate and writable parent (first-run Unraid)", func(t *testing.T) {
		base := t.TempDir()
		mnt := filepath.Join(base, "mnt")
		parent := filepath.Join(mnt, "user")
		leaf := filepath.Join(parent, "Arkeep")
		if err := os.MkdirAll(parent, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(mnt, 0555); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(mnt, 0755) //nolint:errcheck

		if err := ensureWritableDir(leaf); err != nil {
			t.Errorf("expected nil, got: %v", err)
		}
	})

	t.Run("happy path — all writable, dir exists, no leftover temp file", func(t *testing.T) {
		leaf := t.TempDir()

		if err := ensureWritableDir(leaf); err != nil {
			t.Errorf("expected nil, got: %v", err)
		}

		entries, err := os.ReadDir(leaf)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("expected empty dir after call, found %d entries", len(entries))
		}
	})

	t.Run("read-only leaf returns error", func(t *testing.T) {
		leaf := t.TempDir()
		if err := os.Chmod(leaf, 0555); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(leaf, 0755) //nolint:errcheck

		if err := ensureWritableDir(leaf); err == nil {
			t.Error("expected non-nil error for read-only directory")
		}
	})
}

// TestLiveSnapshotIDs verifies that entries with an empty ID are dropped. The
// server treats the returned set as authoritative and evicts every cached
// record outside it, so a malformed entry must never widen the set.
func TestLiveSnapshotIDs(t *testing.T) {
	tests := []struct {
		name      string
		snapshots []restic.SnapshotInfo
		want      []string
	}{
		{
			name:      "all ids kept in order",
			snapshots: []restic.SnapshotInfo{{ID: "aaa"}, {ID: "bbb"}, {ID: "ccc"}},
			want:      []string{"aaa", "bbb", "ccc"},
		},
		{
			name:      "empty id is skipped",
			snapshots: []restic.SnapshotInfo{{ID: "aaa"}, {ID: ""}, {ID: "ccc"}},
			want:      []string{"aaa", "ccc"},
		},
		{
			name:      "empty listing yields empty set",
			snapshots: nil,
			want:      []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := liveSnapshotIDs(tt.snapshots)
			if !slices.Equal(got, tt.want) {
				t.Errorf("liveSnapshotIDs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAnyPathOverlaps(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		want  bool
	}{
		{"unrelated host path", []string{"/opt/apps/example-web/data/content"}, false},
		{"sibling with shared prefix", []string{"/var/lib/docker/volumes-old/x"}, false},
		{"inside root", []string{"/opt/a", "/var/lib/docker/volumes/x/_data"}, true},
		{"root itself", []string{"/var/lib/docker/volumes/"}, true},
		{"ancestor", []string{"/var/lib/docker"}, true},
		{"filesystem root", []string{"/"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := anyPathOverlaps(tt.paths, defaultDockerVolRoot); got != tt.want {
				t.Errorf("anyPathOverlaps(%v) = %v, want %v", tt.paths, got, tt.want)
			}
		})
	}
}

func TestReconcileRestoreFilters(t *testing.T) {
	const vol = "/var/lib/docker/volumes/x/_data"
	tests := []struct {
		name        string
		includes    []string
		excludes    []string
		wantKept    []string
		wantSkipped []string
		wantErr     bool
	}{
		{
			name:     "no excludes — includes unchanged",
			includes: []string{"/opt/a", defaultDockerVolRoot + "/x/_data"},
			wantKept: []string{"/opt/a", defaultDockerVolRoot + "/x/_data"},
		},
		{
			name:     "include outside excluded root is kept",
			includes: []string{"/opt/apps/example-web/data/content"},
			excludes: []string{defaultDockerVolRoot},
			wantKept: []string{"/opt/apps/example-web/data/content"},
		},
		{
			name:     "shared prefix is not inside",
			includes: []string{"/var/lib/docker/volumes-old/x"},
			excludes: []string{defaultDockerVolRoot},
			wantKept: []string{"/var/lib/docker/volumes-old/x"},
		},
		{
			name:        "mix of inside and outside",
			includes:    []string{"/opt/a", vol + "/file", "/opt/b"},
			excludes:    []string{defaultDockerVolRoot},
			wantKept:    []string{"/opt/a", "/opt/b"},
			wantSkipped: []string{vol + "/file"},
		},
		{
			name:        "per-volume exclude skips only that volume",
			includes:    []string{vol + "/file", "/var/lib/docker/volumes/y/_data"},
			excludes:    []string{vol},
			wantKept:    []string{"/var/lib/docker/volumes/y/_data"},
			wantSkipped: []string{vol + "/file"},
		},
		{
			name:     "all includes inside excluded paths",
			includes: []string{vol, vol + "/file"},
			excludes: []string{defaultDockerVolRoot},
			wantErr:  true,
		},
		{"ancestor /var", []string{"/var"}, []string{defaultDockerVolRoot}, nil, nil, true},
		{"ancestor /var/lib/docker", []string{"/opt/a", "/var/lib/docker"}, []string{defaultDockerVolRoot}, nil, nil, true},
		{"ancestor of per-volume exclude", []string{defaultDockerVolRoot}, []string{vol}, nil, nil, true},
		{"filesystem root", []string{"/"}, []string{defaultDockerVolRoot}, nil, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kept, skipped, err := reconcileRestoreFilters(tt.includes, tt.excludes)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !slices.Equal(kept, tt.wantKept) {
				t.Errorf("kept = %v, want %v", kept, tt.wantKept)
			}
			if !slices.Equal(skipped, tt.wantSkipped) {
				t.Errorf("skipped = %v, want %v", skipped, tt.wantSkipped)
			}
		})
	}
}

// TestExecuteBackup_MissingLocalRepositoryIsNotRecreated covers a local
// destination on a disk that is no longer mounted at its path: once the
// repository is known to exist, its missing directory fails the backup instead
// of being created again, and restic is never run against it.
func TestExecuteBackup_MissingLocalRepositoryIsNotRecreated(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls")
	script := filepath.Join(dir, "fake-restic.sh")
	body := "#!/bin/sh\necho \"$1\" >> '" + callLog + "'\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "mnt", "usb")

	payload := backupPayload{
		Sources:      `["/tmp"]`,
		RepoPassword: "pw",
		Destinations: []destinationPayload{{DestinationID: "dest-1", Type: "local", RepoURL: repo, RepoMustExist: true}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	w := restic.NewWrapperWithBinaries(script, "/fake/rclone", zap.NewNop())
	e := New(w, nil, nil, zap.NewNop(), "", "")
	reporter := &fakeReporter{}
	job := JobAssignment{JobID: "job-1", Type: proto.JobType_JOB_TYPE_BACKUP, Payload: raw}

	e.executeBackup(context.Background(), job, fakeSink{}, reporter)

	got := reporter.destResults["dest-1"]
	if got.status != "failed" || !strings.Contains(got.errMsg, "repository not found") {
		t.Errorf("destination result = %+v, want failed with a repository-not-found error", got)
	}
	if _, err := os.Stat(repo); !os.IsNotExist(err) {
		t.Errorf("repository directory was created (stat err = %v), want it left missing", err)
	}
	if data, _ := os.ReadFile(callLog); len(data) > 0 {
		t.Errorf("restic ran %q against a missing repository", data)
	}
}

// TestExecuteBackup_PostHookRunsOnEveryWayOut covers the post-backup hook as
// the counterpart of the pre-backup one (it starts again what that stopped):
// once the job reaches its hooks it runs exactly once, also after a failing
// pre-backup hook and after a cancel, and the job keeps its own outcome.
func TestExecuteBackup_PostHookRunsOnEveryWayOut(t *testing.T) {
	tests := []struct {
		name       string
		preHook    string
		cancelled  bool
		wantStatus string
	}{
		{name: "backup succeeds", wantStatus: "succeeded"},
		{name: "pre-backup hook fails", preHook: `{"command":"exit 1","timeout_secs":10}`, wantStatus: "failed"},
		{name: "job cancelled", cancelled: true, wantStatus: "cancelled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "fake-restic.sh")
			if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(dir, "post-hook-ran")
			payload := backupPayload{
				Sources:        `["/tmp"]`,
				RepoPassword:   "pw",
				Destinations:   []destinationPayload{{DestinationID: "dest-1", Type: "s3", RepoURL: "s3:s3.example.com/b/"}},
				HookPreBackup:  tt.preHook,
				HookPostBackup: `{"command":"echo ran >> '` + marker + `'","timeout_secs":10}`,
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancelled {
				cancel()
			}

			e := New(restic.NewWrapperWithBinaries(script, "/fake/rclone", zap.NewNop()), nil, hooks.NewRunner(10*time.Second), zap.NewNop(), "", "")
			reporter := &fakeReporter{}
			e.executeBackup(ctx, JobAssignment{JobID: "job-1", Type: proto.JobType_JOB_TYPE_BACKUP, Payload: raw}, fakeSink{}, reporter)

			data, _ := os.ReadFile(marker)
			if runs := strings.Count(string(data), "ran"); runs != 1 {
				t.Errorf("post-backup hook ran %d times, want once", runs)
			}
			if final := reporter.statuses[len(reporter.statuses)-1]; final != tt.wantStatus {
				t.Errorf("final job status = %q, want %q (all: %v)", final, tt.wantStatus, reporter.statuses)
			}
		})
	}
}

// TestExecuteBackup_HookCommandStaysOutOfTheJobLog pins that the job log,
// readable by every user, names a hook without its command line, which often
// carries credentials.
func TestExecuteBackup_HookCommandStaysOutOfTheJobLog(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-restic.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	const secret = "hunter2"
	payload := backupPayload{
		Sources:        `["/tmp"]`,
		RepoPassword:   "pw",
		Destinations:   []destinationPayload{{DestinationID: "dest-1", Type: "s3", RepoURL: "s3:s3.example.com/b/"}},
		HookPreBackup:  `{"name":"dump","command":"PGPASSWORD=` + secret + ` true","timeout_secs":10}`,
		HookPostBackup: `{"command":"PGPASSWORD=` + secret + ` true","timeout_secs":10}`,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	e := New(restic.NewWrapperWithBinaries(script, "/fake/rclone", zap.NewNop()), nil, hooks.NewRunner(10*time.Second), zap.NewNop(), "", "")
	sink := &recordingSink{}
	e.executeBackup(context.Background(), JobAssignment{JobID: "job-1", Type: proto.JobType_JOB_TYPE_BACKUP, Payload: raw}, sink, &fakeReporter{})

	all := strings.Join(sink.logs, "\n")
	if strings.Contains(all, secret) {
		t.Errorf("job log contains the hook command:\n%s", all)
	}
	for _, want := range []string{`running pre-backup hook "dump"`, "running post-backup hook"} {
		if !strings.Contains(all, want) {
			t.Errorf("job log lacks %q:\n%s", want, all)
		}
	}
}

// fakeDockerDaemon serves the Docker API on a unix socket and reports every
// volume at mountpoint. Returns a client connected to it.
func fakeDockerDaemon(t *testing.T, mountpoint string) *docker.Client {
	t.Helper()
	// Unix socket paths are limited to about 100 bytes: keep it short.
	dir, err := os.MkdirTemp("", "dk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.47")
		if i := strings.Index(r.URL.Path, "/volumes/"); i >= 0 {
			name := r.URL.Path[i+len("/volumes/"):]
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"Name":%q,"Mountpoint":%q,"Driver":"local"}`, name, mountpoint)
			return
		}
		_, _ = w.Write([]byte("OK"))
	})}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { _ = srv.Close() })
	c, err := docker.NewClient(sock)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestExecuteBackup_UnreachableVolumeFailsTheJob covers a Docker volume whose
// mountpoint is not reachable from the agent (not bind-mounted into its
// container): the other sources are still backed up, but the job fails and
// names the volume, so the failure notification goes out.
func TestExecuteBackup_UnreachableVolumeFailsTheJob(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls")
	script := filepath.Join(dir, "fake-restic.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$@\" >> '"+callLog+"'\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := backupPayload{
		Sources:      `["/tmp","docker-volume://db"]`,
		RepoPassword: "pw",
		Destinations: []destinationPayload{{DestinationID: "dest-1", Type: "s3", RepoURL: "s3:s3.example.com/b/"}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	dockerClient := fakeDockerDaemon(t, filepath.Join(dir, "not-mounted", "_data"))
	e := New(restic.NewWrapperWithBinaries(script, "/fake/rclone", zap.NewNop()), dockerClient, nil, zap.NewNop(), "", "")
	reporter := &fakeReporter{}
	e.executeBackup(context.Background(), JobAssignment{JobID: "job-1", Type: proto.JobType_JOB_TYPE_BACKUP, Payload: raw}, fakeSink{}, reporter)

	if final := reporter.statuses[len(reporter.statuses)-1]; final != "failed" {
		t.Errorf("final job status = %q, want failed (all: %v)", final, reporter.statuses)
	}
	if got := reporter.lastMsg; !strings.Contains(got, "docker-volume://db") {
		t.Errorf("final message = %q, want it to name the skipped volume", got)
	}
	if data, _ := os.ReadFile(callLog); !strings.Contains(string(data), "backup") || !strings.Contains(string(data), "/tmp") {
		t.Errorf("restic calls = %q, want the reachable source backed up", data)
	}
}

// TestEnqueue_IgnoresAJobAlreadyQueued covers the server sending its pending
// jobs again after the stream reconnects: a job already queued is not queued
// twice, and once it has been handled it can be queued again.
func TestEnqueue_IgnoresAJobAlreadyQueued(t *testing.T) {
	e := New(nil, nil, nil, zap.NewNop(), "", "")
	job := JobAssignment{JobID: "job-1", Type: proto.JobType_JOB_TYPE_BACKUP}

	for range 2 {
		if err := e.Enqueue(job); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	if n := len(e.queue); n != 1 {
		t.Fatalf("queue holds %d jobs, want the duplicate ignored", n)
	}

	// Handle it: cancelled while queued, so run reports it without executing.
	e.Cancel(job.JobID)
	reporter := &fakeReporter{}
	e.run(context.Background(), <-e.queue, fakeSink{}, reporter)
	if err := e.Enqueue(job); err != nil || len(e.queue) != 1 {
		t.Errorf("Enqueue after the job was handled: err = %v, queue = %d, want it queued again", err, len(e.queue))
	}
}

// TestEnqueue_FullQueue covers a job that does not fit in the queue: Enqueue
// says so with ErrQueueFull, which the connection manager reports to the
// server as a failed job.
func TestEnqueue_FullQueue(t *testing.T) {
	e := New(nil, nil, nil, zap.NewNop(), "", "")
	for i := range queueSize {
		if err := e.Enqueue(JobAssignment{JobID: fmt.Sprintf("job-%d", i)}); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}
	if err := e.Enqueue(JobAssignment{JobID: "one-too-many"}); !errors.Is(err, ErrQueueFull) {
		t.Errorf("Enqueue on a full queue = %v, want ErrQueueFull", err)
	}
}

func TestNew_NilDockerClientLeavesInterfaceNil(t *testing.T) {
	e := New(nil, nil, nil, zap.NewNop(), "", "")
	if e.docker != nil {
		t.Error("docker interface must stay nil when no client is given")
	}
	if e.dockerVolRoot != "/var/lib/docker/volumes" {
		t.Errorf("dockerVolRoot = %q", e.dockerVolRoot)
	}
}

func TestDesktopHostPath(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/srv/app", "/srv/app"},
		{"/run/desktop/mnt/host/c/Users/u/app", "c:/Users/u/app"},
		{"/host_mnt/d/data", "d:/data"},
		{"/run/desktop/mnt/host/c", "c:/"},
		{"/host_mnt/c/", "c:/"},
		{"/host_mnt", "/host_mnt"},
		{"/host_mntx/c/data", "/host_mntx/c/data"},
	}
	for _, tt := range tests {
		if got := desktopHostPath(tt.in); got != tt.want {
			t.Errorf("desktopHostPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIsFilesystemRoot(t *testing.T) {
	for p, want := range map[string]bool{
		"/": true, `C:\`: true, "C:/": true, "c:": true, "c:/": true,
		"/srv": false, "C:/Users": false, `C:\Users`: false,
	} {
		if got := isFilesystemRoot(p); got != want {
			t.Errorf("isFilesystemRoot(%q) = %v, want %v", p, got, want)
		}
	}
}
