// Package restictest provides helpers for tests that drive the real embedded
// restic binary against local repositories: a shared wrapper, a deterministic
// fixture tree, and a tree snapshot/compare facility.
package restictest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/agent/internal/restic"
)

const repoPassword = "test-password"

var (
	once       sync.Once
	stateDir   string
	wrapper    *restic.Wrapper
	wrapperErr error
)

// Wrapper returns a Wrapper backed by the real embedded restic binary. The
// binaries are extracted once per test process; call Cleanup from TestMain.
// It also points RESTIC_CACHE_DIR at a per-process directory, so restic never
// touches the user's cache.
func Wrapper(t testing.TB) *restic.Wrapper {
	t.Helper()
	once.Do(func() {
		stateDir, wrapperErr = os.MkdirTemp("", "arkeep-restictest-*")
		if wrapperErr != nil {
			return
		}
		// restic inherits the test process env: keep its cache out of the
		// developer's ~/.cache/restic. Cleanup removes it with stateDir.
		if wrapperErr = os.Setenv("RESTIC_CACHE_DIR", filepath.Join(stateDir, "cache")); wrapperErr != nil {
			return
		}
		wrapper, wrapperErr = restic.NewWrapper(restic.NewExtractor(stateDir), zap.NewNop())
	})
	if wrapperErr != nil {
		t.Fatalf("restictest: prepare restic wrapper: %v", wrapperErr)
	}
	return wrapper
}

// Cleanup removes the directory the binaries were extracted to. Call it from
// TestMain after m.Run().
func Cleanup() {
	if stateDir != "" {
		_ = os.RemoveAll(stateDir)
	}
}

// NewLocalRepo returns a local destination in a fresh temp dir. The
// repository is not initialised; the first backup creates it.
func NewLocalRepo(t testing.TB) restic.Destination {
	t.Helper()
	return restic.Destination{
		Type:     restic.DestLocal,
		RepoURL:  t.TempDir() + "/repo",
		Password: repoPassword,
	}
}

var (
	templateOnce sync.Once
	templateDir  string
	templateErr  error
)

// NewInitializedLocalRepo is like NewLocalRepo but the repository already
// exists. Initialising a repository costs ~2.5s of key derivation, so it is
// done once per process and copied for each caller; use it where the test is
// not about repository creation.
func NewInitializedLocalRepo(t testing.TB) restic.Destination {
	t.Helper()
	dest := NewLocalRepo(t)
	CopyInitializedRepo(t, dest.RepoURL)
	return dest
}

// CopyInitializedRepo creates an initialised repository (password
// "test-password") at dst, which must not exist yet. Use it where the caller
// decides the repository location, e.g. a path the agent translates.
func CopyInitializedRepo(t testing.TB, dst string) {
	t.Helper()
	w := Wrapper(t)
	templateOnce.Do(func() {
		templateDir = filepath.Join(stateDir, "repo-template")
		templateErr = w.Init(context.Background(), restic.Destination{
			Type: restic.DestLocal, RepoURL: templateDir, Password: repoPassword,
		})
	})
	if templateErr != nil {
		t.Fatalf("restictest: init template repository: %v", templateErr)
	}
	if err := os.CopyFS(dst, os.DirFS(templateDir)); err != nil {
		t.Fatalf("restictest: copy template repository: %v", err)
	}
}

func mustWrite(t testing.TB, path string, data []byte, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("restictest: mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatalf("restictest: write %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("restictest: chmod %s: %v", path, err)
	}
}

// WriteTree writes the standard fixture tree under root.
func WriteTree(t testing.TB, root string) {
	t.Helper()
	mustWrite(t, filepath.Join(root, "a.txt"), []byte("hello arkeep\n"), 0o644)
	mustWrite(t, filepath.Join(root, "dir with space", "ünïcödé.txt"), []byte("unicode\n"), 0o644)

	// 3 MiB of deterministic data so the file spans several restic chunks.
	big := make([]byte, 3<<20)
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < len(big); i += 8 {
		v := rng.Uint64()
		for j := 0; j < 8; j++ {
			big[i+j] = byte(v >> (8 * j))
		}
	}
	mustWrite(t, filepath.Join(root, "nested", "deep", "er", "file.bin"), big, 0o644)

	mustWrite(t, filepath.Join(root, "empty.txt"), nil, 0o644)
	if err := os.MkdirAll(filepath.Join(root, "empty-dir"), 0o755); err != nil {
		t.Fatalf("restictest: mkdir empty-dir: %v", err)
	}
	if err := os.Symlink("a.txt", filepath.Join(root, "link-to-a")); err != nil {
		t.Fatalf("restictest: symlink: %v", err)
	}

	mustWrite(t, filepath.Join(root, "private.txt"), []byte("secret\n"), 0o600)
	mustWrite(t, filepath.Join(root, "private-dir", "inside.txt"), []byte("inside\n"), 0o644)
	if err := os.Chmod(filepath.Join(root, "private-dir"), 0o700); err != nil {
		t.Fatalf("restictest: chmod private-dir: %v", err)
	}

	for i := 0; i < 1000; i++ {
		mustWrite(t, filepath.Join(root, "many", fmt.Sprintf("f%04d.txt", i)), []byte(fmt.Sprintf("%d\n", i)), 0o644)
	}
}

// DamageTree mutates a tree written by WriteTree so a restore has real work
// to do: content change, deletions, a mode change and an unrelated new file.
func DamageTree(t testing.TB, root string) {
	t.Helper()
	mustWrite(t, filepath.Join(root, "a.txt"), []byte("damaged\n"), 0o644)
	for _, p := range []string{"nested/deep/er/file.bin", "link-to-a"} {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(p))); err != nil {
			t.Fatalf("restictest: remove %s: %v", p, err)
		}
	}
	if err := os.Chmod(filepath.Join(root, "private.txt"), 0o644); err != nil {
		t.Fatalf("restictest: chmod private.txt: %v", err)
	}
	mustWrite(t, filepath.Join(root, "extra.txt"), []byte("extra\n"), 0o644)
}

// Entry describes one filesystem node in a Tree.
type Entry struct {
	Type    string // "file", "dir" or "symlink"
	Mode    fs.FileMode
	SHA256  string // files only
	Link    string // symlinks only
	ModTime int64  // Unix seconds, files only
}

// Tree maps slash-separated paths relative to the captured root to entries.
type Tree map[string]Entry

// Capture records every node under root (the root itself is ignored).
func Capture(t testing.TB, root string) Tree {
	t.Helper()
	tree := Tree{}
	err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		e := Entry{Mode: info.Mode().Perm()}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			e.Type = "symlink"
			e.Mode = 0
			if e.Link, err = os.Readlink(path); err != nil {
				return err
			}
		case info.IsDir():
			e.Type = "dir"
		default:
			e.Type = "file"
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			e.SHA256 = hex.EncodeToString(sum[:])
			e.ModTime = info.ModTime().Unix()
		}
		tree[filepath.ToSlash(rel)] = e
		return nil
	})
	if err != nil {
		t.Fatalf("restictest: capture %s: %v", root, err)
	}
	return tree
}

// AssertTreeMatches fails the test if the tree under root differs from want
// plus exactly the extra paths. Every difference is reported.
func AssertTreeMatches(t testing.TB, want Tree, root string, extra ...string) {
	t.Helper()
	got := Capture(t, root)
	allowed := map[string]bool{}
	for _, p := range extra {
		allowed[p] = true
	}
	var diffs []string
	for p, w := range want {
		g, ok := got[p]
		if !ok {
			diffs = append(diffs, "missing: "+p)
			continue
		}
		if g != w {
			diffs = append(diffs, fmt.Sprintf("differs: %s: want %+v, got %+v", p, w, g))
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok && !allowed[p] {
			diffs = append(diffs, "unexpected: "+p)
		}
	}
	for p := range allowed {
		if _, ok := got[p]; !ok {
			diffs = append(diffs, "missing extra: "+p)
		}
	}
	if len(diffs) > 0 {
		sort.Strings(diffs)
		t.Errorf("tree under %s does not match (%d differences):\n%s", root, len(diffs), strings.Join(diffs, "\n"))
	}
}
