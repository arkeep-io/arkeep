//go:build !windows

package executor

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/agent/internal/restic"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// mkdirMode creates dir with exactly mode, whatever the umask.
func mkdirMode(t *testing.T, dir string, mode fs.FileMode) {
	t.Helper()
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
}

func TestSecureRestoreTarget(t *testing.T) {
	t.Run("creates a missing folder private", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "restore", "here")
		if err := secureRestoreTarget(dir); err != nil {
			t.Fatalf("secureRestoreTarget: %v", err)
		}
		fi, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o700 {
			t.Errorf("mode = %o, want 700", perm)
		}
	})

	t.Run("accepts an existing folder of the agent", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "restore")
		mkdirMode(t, dir, 0o755)
		if err := secureRestoreTarget(dir); err != nil {
			t.Errorf("secureRestoreTarget: %v", err)
		}
	})

	t.Run("accepts a sticky world-writable parent, like /tmp", func(t *testing.T) {
		shared := filepath.Join(t.TempDir(), "tmp")
		mkdirMode(t, shared, 0o777|fs.ModeSticky)
		if err := secureRestoreTarget(filepath.Join(shared, "arkeep-restore")); err != nil {
			t.Errorf("secureRestoreTarget: %v", err)
		}
	})

	refused := []struct {
		name  string
		setup func(t *testing.T, base string) string
		want  string
	}{
		{"a folder writable by others", func(t *testing.T, base string) string {
			dir := filepath.Join(base, "restore")
			mkdirMode(t, dir, 0o777)
			return dir
		}, "writable by other users"},
		{"a sticky folder writable by others", func(t *testing.T, base string) string {
			dir := filepath.Join(base, "restore")
			mkdirMode(t, dir, 0o777|fs.ModeSticky)
			return dir
		}, "writable by other users"},
		{"a parent writable by others without sticky bit", func(t *testing.T, base string) string {
			parent := filepath.Join(base, "open")
			mkdirMode(t, parent, 0o777)
			return filepath.Join(parent, "restore")
		}, "writable by other users"},
		{"a file", func(t *testing.T, base string) string {
			f := filepath.Join(base, "file")
			if err := os.WriteFile(f, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			return f
		}, "not a directory"},
		{"a link to a folder writable by others", func(t *testing.T, base string) string {
			open := filepath.Join(base, "open")
			mkdirMode(t, open, 0o777)
			link := filepath.Join(base, "restore")
			if err := os.Symlink(open, link); err != nil {
				t.Fatal(err)
			}
			return link
		}, "writable by other users"},
		{"a relative path", func(*testing.T, string) string { return "restore" }, "absolute"},
	}
	for _, tt := range refused {
		t.Run("refuses "+tt.name, func(t *testing.T) {
			dir := tt.setup(t, t.TempDir())
			err := secureRestoreTarget(dir)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("secureRestoreTarget = %v, want an error containing %q", err, tt.want)
			}
		})
	}

	t.Run("refuses before creating anything under an unsafe parent", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), "open")
		mkdirMode(t, parent, 0o777)
		dir := filepath.Join(parent, "restore")
		if err := secureRestoreTarget(dir); err == nil {
			t.Fatal("secureRestoreTarget accepted the path")
		}
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			t.Errorf("the target was created under an unsafe parent (stat err = %v)", err)
		}
	})
}

// ownedInfo is a FileInfo of a directory owned by uid, to test ownership
// without root.
type ownedInfo struct {
	mode fs.FileMode
	uid  uint32
}

func (i ownedInfo) Name() string       { return "d" }
func (i ownedInfo) Size() int64        { return 0 }
func (i ownedInfo) Mode() fs.FileMode  { return i.mode }
func (i ownedInfo) ModTime() time.Time { return time.Time{} }
func (i ownedInfo) IsDir() bool        { return i.mode.IsDir() }
func (i ownedInfo) Sys() any           { return &syscall.Stat_t{Uid: i.uid} }

func TestCheckRestoreComponent_Owner(t *testing.T) {
	const agent = 1000
	tests := []struct {
		name string
		info ownedInfo
		ok   bool
	}{
		{"root", ownedInfo{fs.ModeDir | 0o755, 0}, true},
		{"the agent's user", ownedInfo{fs.ModeDir | 0o700, agent}, true},
		{"another user", ownedInfo{fs.ModeDir | 0o700, 4242}, false},
		{"a link of another user", ownedInfo{fs.ModeSymlink | 0o777, 4242}, false},
		{"a link of root", ownedInfo{fs.ModeSymlink | 0o777, 0}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkRestoreComponent("/x", tt.info, agent, false)
			if (err == nil) != tt.ok {
				t.Errorf("checkRestoreComponent = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

// TestExecuteRestore_UnsafeTargetFailsBeforeRestic covers P-03 end to end: a
// restore into a folder other users can write to fails, and restic never runs.
func TestExecuteRestore_UnsafeTargetFailsBeforeRestic(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := filepath.Join(dir, "fake-restic.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$1\" >> '"+calls+"'\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "restore")
	mkdirMode(t, target, 0o777)

	raw, err := json.Marshal(restorePayload{
		ResticSnapshotID: "3f2a9c1e5b7d",
		RepoPassword:     "pw",
		TargetPath:       target,
		Destination:      destinationPayload{DestinationID: "dest-1", Type: "s3", RepoURL: "s3:s3.example.com/b/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	e := New(restic.NewWrapperWithBinaries(script, "/fake/rclone", zap.NewNop()), nil, nil, zap.NewNop(), "", "")
	reporter := &fakeReporter{}
	e.executeRestore(context.Background(), JobAssignment{JobID: "restore-1", Type: proto.JobType_JOB_TYPE_RESTORE, Payload: raw}, fakeSink{}, reporter)

	if final := reporter.statuses[len(reporter.statuses)-1]; final != "failed" || !strings.Contains(reporter.lastMsg, "writable by other users") {
		t.Errorf("final status = %q (%s), want failed because of the target", final, reporter.lastMsg)
	}
	if data, _ := os.ReadFile(calls); len(data) > 0 {
		t.Errorf("restic ran (%q) although the target was unsafe", data)
	}
}
