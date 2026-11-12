package restic_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/arkeep-io/arkeep/agent/internal/restic"
	"github.com/arkeep-io/arkeep/agent/internal/restic/restictest"
)

// backedUp backs up a fresh fixture tree into a new local repository and
// returns everything a round-trip test needs.
func backedUp(t *testing.T) (w *restic.Wrapper, dest restic.Destination, src, snapID string, want restictest.Tree) {
	t.Helper()
	w = restictest.Wrapper(t)
	dest = restictest.NewInitializedLocalRepo(t)
	src = filepath.Join(t.TempDir(), "src")
	restictest.WriteTree(t, src)
	want = restictest.Capture(t, src)

	res, err := w.Backup(context.Background(), dest, restic.BackupOptions{Sources: []string{src}}, nil)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if res.SnapshotID == "" {
		t.Fatal("backup returned an empty snapshot id")
	}
	return w, dest, src, res.SnapshotID, want
}

func TestRestore_ToFolder_WholeSnapshot(t *testing.T) {
	w, dest, src, id, want := backedUp(t)
	target := t.TempDir()
	if err := w.Restore(context.Background(), dest, id, target, nil, nil, ""); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restictest.AssertTreeMatches(t, want, filepath.Join(target, src))
}

func TestRestore_ToFolder_Latest(t *testing.T) {
	w, dest, src, _, want := backedUp(t)
	target := t.TempDir()
	if err := w.Restore(context.Background(), dest, "latest", target, nil, nil, ""); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restictest.AssertTreeMatches(t, want, filepath.Join(target, src))
}

func TestRestore_ToFolder_IncludeFromLs(t *testing.T) {
	w, dest, src, id, want := backedUp(t)
	ctx := context.Background()

	entries, err := w.Ls(ctx, dest, id, src)
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

	target := t.TempDir()
	if err := w.Restore(ctx, dest, id, target, []string{include}, nil, ""); err != nil {
		t.Fatalf("restore: %v", err)
	}

	subWant := restictest.Tree{}
	for p, e := range want {
		if p == "dir with space" || strings.HasPrefix(p, "dir with space/") {
			subWant[p] = e
		}
	}
	if len(subWant) != 2 {
		t.Fatalf("fixture subset has %d entries, want 2", len(subWant))
	}
	// Only the included subtree may exist under the recreated source path.
	restictest.AssertTreeMatches(t, subWant, filepath.Join(target, src))
	for p := range restictest.Capture(t, filepath.Join(target, src)) {
		if p != "dir with space" && !strings.HasPrefix(p, "dir with space/") {
			t.Errorf("restored %q outside the included directory", p)
		}
	}
}

func TestRestore_InPlace(t *testing.T) {
	w, dest, src, id, want := backedUp(t)
	restictest.DamageTree(t, src)
	if err := w.Restore(context.Background(), dest, id, "/", nil, nil, ""); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restictest.AssertTreeMatches(t, want, src, "extra.txt")
}

func TestRestore_IncludeMatchesNothing(t *testing.T) {
	w, dest, src, id, _ := backedUp(t)
	err := w.Restore(context.Background(), dest, id, t.TempDir(), []string{src + "/does-not-exist"}, nil, "")
	if !errors.Is(err, restic.ErrNothingRestored) {
		t.Fatalf("restore error = %v, want ErrNothingRestored", err)
	}
}

func TestLs_ListsBackedUpEntries(t *testing.T) {
	w, dest, src, id, want := backedUp(t)
	entries, err := w.Ls(context.Background(), dest, id, src)
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	var got, exp []string
	for _, e := range entries {
		got = append(got, filepath.Base(e.Path))
	}
	for p := range want {
		if !strings.Contains(p, "/") {
			exp = append(exp, p)
		}
	}
	sort.Strings(got)
	sort.Strings(exp)
	if strings.Join(got, "\x00") != strings.Join(exp, "\x00") {
		t.Fatalf("ls entries = %q, want %q", got, exp)
	}
}

func TestDump_File(t *testing.T) {
	w, dest, src, id, want := backedUp(t)
	var buf bytes.Buffer
	if err := w.Dump(context.Background(), dest, id, src+"/nested/deep/er/file.bin", false, &buf); err != nil {
		t.Fatalf("dump: %v", err)
	}
	sum := sha256.Sum256(buf.Bytes())
	if got := hex.EncodeToString(sum[:]); got != want["nested/deep/er/file.bin"].SHA256 {
		t.Fatalf("dumped sha256 = %s, want %s", got, want["nested/deep/er/file.bin"].SHA256)
	}
}

func TestDump_DirArchive(t *testing.T) {
	w, dest, src, id, _ := backedUp(t)
	var buf bytes.Buffer
	if err := w.Dump(context.Background(), dest, id, src+"/private-dir", true, &buf); err != nil {
		t.Fatalf("dump: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	var names []string
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		names = append(names, f.Name)
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		if string(data) != "inside\n" {
			t.Errorf("%s = %q, want %q", f.Name, data, "inside\n")
		}
	}
	if len(names) != 1 || filepath.Base(names[0]) != "inside.txt" || !strings.Contains(names[0], "private-dir") {
		t.Fatalf("zip file entries = %q, want exactly private-dir/inside.txt", names)
	}
}
