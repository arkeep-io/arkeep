package restic_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arkeep-io/arkeep/agent/internal/restic"
	"github.com/arkeep-io/arkeep/shared/repair"
)

// largestPack returns the path of the biggest file under the repository's
// data directory: the pack holding the fixture's 3 MiB file, rather than a
// small pack of trees.
func largestPack(t *testing.T, repo string) string {
	t.Helper()
	var path string
	var size int64
	err := filepath.WalkDir(filepath.Join(repo, "data"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > size {
			path, size = p, info.Size()
		}
		return nil
	})
	if err != nil || path == "" {
		t.Fatalf("no pack file under %s: %v", repo, err)
	}
	return path
}

func run(t *testing.T, w *restic.Wrapper, dest restic.Destination, p repair.Params) []string {
	t.Helper()
	var lines []string
	if err := w.Repair(context.Background(), dest, p, func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatalf("%s: %v\noutput:\n%s", p.Action, err, strings.Join(lines, "\n"))
	}
	return lines
}

// TestRepair_LostPack follows restic's procedure for a repository that lost
// a pack file: a check finds the damage, repair index drops the missing pack
// from the index, a dry run of repair snapshots changes nothing, and repair
// snapshots --forget rewrites the snapshot without the lost file. The
// repository then passes a full check.
func TestRepair_LostPack(t *testing.T) {
	w, dest, _, _, _ := backedUp(t)
	ctx := context.Background()

	lost := largestPack(t, dest.RepoURL)
	if err := os.Remove(lost); err != nil {
		t.Fatal(err)
	}

	if err := w.Repair(ctx, dest, repair.Params{Action: repair.Check}, nil); err == nil {
		t.Fatal("check passed on a repository that lost a pack")
	} else if code := restic.ExitCode(err); code != 1 {
		t.Errorf("check exit code = %d, want 1 (errors found)", code)
	}

	if lines := run(t, w, dest, repair.Params{Action: repair.Index}); len(lines) == 0 {
		t.Error("repair index streamed no output")
	}

	before, err := w.Snapshots(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	run(t, w, dest, repair.Params{Action: repair.Snapshots})
	after, err := w.Snapshots(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) || after[0].ID != before[0].ID {
		t.Fatalf("the dry run changed the snapshots: %d before, %d after", len(before), len(after))
	}

	run(t, w, dest, repair.Params{Action: repair.Snapshots, Apply: true, Forget: true})
	repaired, err := w.Snapshots(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(repaired) != 1 || repaired[0].ID == before[0].ID {
		t.Fatalf("snapshots after repair = %+v, want one rewritten snapshot replacing %s", repaired, before[0].ID)
	}

	run(t, w, dest, repair.Params{Action: repair.Check, ReadData: true})
}

// TestRepair_FindPacks lists what depends on a pack without changing the
// repository: the preview the GUI shows before repair-packs.
func TestRepair_FindPacks(t *testing.T) {
	w, dest, _, _, _ := backedUp(t)
	pack := filepath.Base(largestPack(t, dest.RepoURL))

	lines := run(t, w, dest, repair.Params{Action: repair.FindPacks, PackIDs: []string{pack}})
	if !strings.Contains(strings.Join(lines, "\n"), "file.bin") {
		t.Errorf("find --pack output does not name the file stored in the pack:\n%s", strings.Join(lines, "\n"))
	}
	run(t, w, dest, repair.Params{Action: repair.Check})
}

// TestRepair_LockedRepository: a lock held by a running operation is
// reported with restic's exit code for it, and the unlock action leaves it
// alone, since it only removes stale locks.
func TestRepair_LockedRepository(t *testing.T) {
	w, dest, _, _, _ := backedUp(t)
	ctx := context.Background()
	lockDir := filepath.Join(dest.RepoURL, "locks")

	// A backup reading from a command that waits holds a lock meanwhile.
	backupDone := make(chan error, 1)
	go func() {
		_, err := w.BackupFromCommand(ctx, dest, restic.StdinBackupOptions{Command: "sleep 4; echo done", Filename: "wait"}, nil)
		backupDone <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if entries, _ := os.ReadDir(lockDir); len(entries) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the backup never took its lock")
		}
		time.Sleep(50 * time.Millisecond)
	}

	err := w.Repair(ctx, dest, repair.Params{Action: repair.Index}, nil)
	if code := restic.ExitCode(err); code != 11 {
		t.Errorf("repair index on a locked repository: exit code %d (%v), want 11", code, err)
	}
	if err := w.Repair(ctx, dest, repair.Params{Action: repair.Unlock}, nil); err != nil {
		t.Errorf("unlock: %v", err)
	}
	if entries, _ := os.ReadDir(lockDir); len(entries) == 0 {
		t.Error("unlock removed the lock of a running backup")
	}
	if err := <-backupDone; err != nil {
		t.Fatalf("backup: %v", err)
	}
}
