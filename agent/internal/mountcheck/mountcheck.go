// Package mountcheck tells whether a path inside a container is on storage
// that survives the container being recreated.
//
// A path a container can write to but that is not on a bind mount or a volume
// lives in the container's own writable layer (or in a tmpfs): writing there
// succeeds, and the data is gone when the container is recreated. The kernel
// lists the mounts of the process in /proc/self/mountinfo; the mount holding a
// path is the one with the longest mount point that contains it.
package mountcheck

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ProcMountInfo is where Linux lists the mounts of the calling process.
const ProcMountInfo = "/proc/self/mountinfo"

// Mount is one line of a mountinfo file.
type Mount struct {
	Point  string // mount point, as seen by the process
	FSType string // filesystem type, e.g. "overlay", "ext4", "tmpfs"
}

// ParseMountInfo reads the format of /proc/<pid>/mountinfo (see proc(5)):
//
//	36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw
//
// The mount point is the fifth field. A variable number of optional fields
// follows the sixth, up to the "-" separator; the filesystem type comes right
// after it.
func ParseMountInfo(r io.Reader) ([]Mount, error) {
	var mounts []Mount
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // long overlay option lists
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		sep := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || sep+1 >= len(fields) {
			return nil, fmt.Errorf("malformed mountinfo line %q", line)
		}
		mounts = append(mounts, Mount{Point: unescape(fields[4]), FSType: fields[sep+1]})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return mounts, nil
}

// unescape decodes the octal escapes the kernel writes for space, tab,
// newline and backslash in a mount point (\040, \011, \012, \134).
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Ephemeral reports whether path, inside a container, is on storage that is
// lost when the container is recreated: the container's own root filesystem,
// or a tmpfs or ramfs. It returns a description of that storage for an error
// message, or "" when path is on a bind mount or a volume.
//
// path does not have to exist: the repository directory of a first backup is
// created later, under its nearest existing ancestor, so that ancestor (with
// symlinks resolved) is what is looked up. mountinfo is the file to read,
// ProcMountInfo outside tests.
func Ephemeral(mountinfo, path string) (string, error) {
	f, err := os.Open(filepath.Clean(mountinfo))
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck // read-only
	mounts, err := ParseMountInfo(f)
	if err != nil {
		return "", err
	}
	return ephemeralKind(mounts, existingAncestor(path)), nil
}

// ephemeralKind looks up the mount holding path (absolute, clean, symlinks
// resolved) and describes it when it does not outlive the container.
// Mounts are in mount order, so when two share a mount point the later one,
// mounted over the other, wins.
func ephemeralKind(mounts []Mount, path string) string {
	var best *Mount
	for i := range mounts {
		m := &mounts[i]
		if !contains(m.Point, path) {
			continue
		}
		if best == nil || len(m.Point) >= len(best.Point) {
			best = m
		}
	}
	switch {
	case best == nil:
		return ""
	case best.FSType == "tmpfs" || best.FSType == "ramfs":
		return fmt.Sprintf("a %s mount at %s, kept in memory", best.FSType, best.Point)
	case best.Point == "/":
		// The root of a container is its image plus its writable layer,
		// whatever the storage driver (overlay, btrfs, zfs, vfs…).
		return fmt.Sprintf("the container's own filesystem (%s)", best.FSType)
	}
	return ""
}

// contains reports whether path is mount point point or under it.
func contains(point, path string) bool {
	return point == "/" || path == point || strings.HasPrefix(path, point+"/")
}

// existingAncestor returns the nearest ancestor of path that exists (path
// itself if it does), with symlinks resolved.
func existingAncestor(path string) string {
	p, err := filepath.Abs(path)
	if err != nil {
		p = filepath.Clean(path)
	}
	for {
		if _, err := os.Lstat(p); err == nil {
			break
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}
