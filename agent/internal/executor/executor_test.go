package executor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/agent/internal/restic"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

type fakeSink struct{}

func (fakeSink) SendLog(jobID, level, message string) {}

type destResult struct {
	status string
	errMsg string
}

type fakeReporter struct {
	statuses    []string
	destResults map[string]destResult
}

func (r *fakeReporter) ReportStatus(jobID, status, message string) {
	r.statuses = append(r.statuses, status)
}

func (r *fakeReporter) ReportDestinationResult(jobID, destinationID, status, snapshotID string, startedAt time.Time, sizeBytes, repoSizeBytes int64, errMsg string) {
	if r.destResults == nil {
		r.destResults = make(map[string]destResult)
	}
	r.destResults[destinationID] = destResult{status: status, errMsg: errMsg}
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

	e := New(nil, nil, nil, zap.NewNop(), "")
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

// TestResolveSources_RejectsFlagLikeEntries is a regression test for
// GHSA-263g-c333-jcjq / GHSA-75rg-4ppf-pq7g: resolveSources is the agent-side
// defense-in-depth gate that must still reject a flag-like source even if a
// malicious policy predates server-side validation.
func TestResolveSources_RejectsFlagLikeEntries(t *testing.T) {
	e := New(nil, nil, nil, zap.NewNop(), "")
	noopLog := func(level, msg string) {}

	_, err := e.resolveSources(context.Background(), `["--password-command=touch /tmp/pwned"]`, noopLog)
	if err == nil {
		t.Fatal("expected an error rejecting a flag-like source, got nil")
	}
}

// TestResolveSources_AcceptsNormalPaths guards against over-restricting: the
// flag-like check must not reject legitimate source paths.
func TestResolveSources_AcceptsNormalPaths(t *testing.T) {
	e := New(nil, nil, nil, zap.NewNop(), "")
	noopLog := func(level, msg string) {}

	got, err := e.resolveSources(context.Background(), `["/data", "C:\\Users"]`, noopLog)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"/data", `C:\Users`}
	if !slices.Equal(got, want) {
		t.Errorf("resolveSources() = %v, want %v", got, want)
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
