package executor

import (
	"os"
	"path/filepath"
	"testing"
)

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
