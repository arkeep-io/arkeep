//go:build !windows

package executor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// secureRestoreTarget prepares dir, the folder a restore writes into, so that
// no other local user can read what is restored or redirect the agent's
// writes (P-03). The agent often runs as root, and the default target sits in
// /tmp, where anyone can create /tmp/arkeep-restore first.
//
// A missing dir is created with mode 0700. Every existing component of the
// path, dir included, must belong to root or to the agent's user, and must not
// be writable by group or others, except for a sticky directory such as /tmp,
// where others cannot rename or remove what they do not own; dir itself must
// not be writable by others at all. A symbolic link is accepted only when root
// or the agent's user owns it, and the path it leads to is checked too
// (macOS: /tmp → /private/tmp). The same rules as ssh's StrictModes.
func secureRestoreTarget(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("restore target %q must be an absolute path", dir)
	}
	dir = filepath.Clean(dir)
	uid := os.Geteuid()

	// Before creating anything: refuse a path another user already controls.
	if err := checkRestorePath(dir, uid, false); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("restore target %q: %w", dir, err)
	}
	// Again, now that the whole path exists: a component another user created
	// in a sticky directory between the two checks is caught here.
	if err := checkRestorePath(dir, uid, true); err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("restore target %q: %w", dir, err)
	}
	if real != dir {
		return checkRestorePath(real, uid, true)
	}
	return nil
}

// checkRestorePath checks every existing component of dir, from the root
// down. With mustExist false, it stops at the first missing component, which
// secureRestoreTarget then creates.
func checkRestorePath(dir string, uid int, mustExist bool) error {
	p := string(filepath.Separator)
	parts := strings.Split(strings.TrimPrefix(dir, p), p)
	for i := -1; i < len(parts); i++ {
		if i >= 0 {
			p = filepath.Join(p, parts[i])
		}
		fi, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) && !mustExist {
			return nil
		}
		if err != nil {
			return fmt.Errorf("restore target %q: %w", dir, err)
		}
		if err := checkRestoreComponent(p, fi, uid, p == dir); err != nil {
			return fmt.Errorf("restore target %q is not safe: %w; choose another folder", dir, err)
		}
	}
	return nil
}

// checkRestoreComponent applies the rules of secureRestoreTarget to one
// component of the path. target is true for the restore folder itself.
func checkRestoreComponent(p string, fi fs.FileInfo, uid int, target bool) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot read the owner of %s", p)
	}
	// Compared as int64, which holds both the uid_t of Stat_t and the int of
	// Geteuid without overflow.
	if owner := int64(st.Uid); owner != 0 && owner != int64(uid) {
		return fmt.Errorf("%s belongs to another user (uid %d)", p, owner)
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return nil // its own target is checked through EvalSymlinks
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", p)
	}
	if fi.Mode().Perm()&0o022 != 0 && (target || fi.Mode()&fs.ModeSticky == 0) {
		return fmt.Errorf("%s is writable by other users", p)
	}
	return nil
}
