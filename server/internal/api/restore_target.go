package api

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/arkeep-io/arkeep/server/internal/policyutil"
)

// restoreSelection returns the paths a restore writes: the include paths when
// the caller selected some, otherwise the snapshot's sources as stored on
// db.Snapshot.Sources. Imported snapshots store a plain JSON array of paths;
// snapshots made by a policy store a copy of the policy's sources, the GUI's
// object form ([{"type":"directory","path":"/srv/app"}]), which is read with
// policyutil.SourcePaths (docker volumes become docker-volume:// URIs and
// command sources are dropped). Sources in neither form yield nil.
func restoreSelection(includePaths []string, sourcesJSON string) []string {
	if len(includePaths) > 0 {
		return includePaths
	}
	var sources []string
	if err := json.Unmarshal([]byte(sourcesJSON), &sources); err == nil {
		return sources
	}
	sources, err := policyutil.SourcePaths(sourcesJSON)
	if err != nil {
		return nil
	}
	return sources
}

// nestedRestoreTarget reports whether restoring paths to the folder target
// would nest them inside their own original location. restic recreates each
// path's full absolute path below the target, so restoring /data/app to
// /data/app writes /data/app/data/app and leaves the original files as they
// were, while the job succeeds. The in-place sentinel "/" is never nested.
// When nested is true, msg explains what to do instead.
//
// Windows: a drive target (C:\Users\me) is compared with paths in either the
// drive form (C:\Users\me\Documents, as in policy sources) or restic's form
// (/C/Users/me/Documents, as sent by the snapshot browser). The restore
// dialog offers no in-place restore on Windows agents, so the message then
// only suggests a folder outside the target.
//
// Known limits: entries that are not absolute paths (docker-volume:// and
// command sources) are skipped, and the paths of snapshots imported from a
// Docker agent carry the agent's host root (/hostfs/...), which the server
// does not know. For include paths the agent checks again after its own
// path translation; a whole-snapshot restore of such sources is not checked.
func nestedRestoreTarget(target string, paths []string) (msg string, nested bool) {
	t, ok := comparablePath(target)
	if !ok || t == "/" {
		return "", false
	}
	windows := isDrivePath(t)
	for _, p := range paths {
		c, ok := comparablePath(p)
		if !ok {
			continue
		}
		if windows {
			c = fromResticWindowsPath(c)
		}
		if !pathWithin(c, t) {
			continue
		}
		if windows {
			return fmt.Sprintf(
				"target_path %q is the original location of %q or a folder above it: restoring there would write the files to %q "+
					"and leave the originals unchanged. Choose a folder outside %q",
				target, p, path.Join(t, toResticWindowsPath(c)), target), true
		}
		return fmt.Sprintf(
			"target_path %q is the original location of %q or a folder above it: restoring there would write the files to %q "+
				"and leave the originals unchanged. To put the files back where they were, restore to the original location "+
				"(target_path \"/\"); to keep a separate copy, choose a folder outside %q",
			target, p, path.Join(t, c), target), true
	}
	return "", false
}

// comparablePath returns p with forward slashes, cleaned, when it is an
// absolute Unix path or a Windows drive path.
func comparablePath(p string) (string, bool) {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	if !strings.HasPrefix(p, "/") && !isDrivePath(p) {
		return "", false
	}
	c := path.Clean(p)
	if len(c) == 2 && c[1] == ':' { // path.Clean("C:/") is "C:"
		c += "/"
	}
	return c, true
}

// isDrivePath reports whether p (forward slashes) starts with a drive, as in
// C:/Users.
func isDrivePath(p string) bool {
	return len(p) >= 3 && isDriveLetter(p[0]) && p[1] == ':' && p[2] == '/'
}

func isDriveLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// fromResticWindowsPath turns restic's form of a Windows path (/C/Users) into
// the drive form (C:/Users). Other paths are returned unchanged. Only call it
// when the target is a drive path, since /C/Users is also a valid Unix path.
func fromResticWindowsPath(p string) string {
	if len(p) >= 2 && p[0] == '/' && isDriveLetter(p[1]) && (len(p) == 2 || p[2] == '/') {
		return p[1:2] + ":" + path.Clean("/"+p[2:])
	}
	return p
}

// toResticWindowsPath turns a drive path (C:/Users) into the form restic
// recreates below a target (/C/Users).
func toResticWindowsPath(p string) string {
	if isDrivePath(p) {
		return "/" + p[:1] + p[2:]
	}
	return p
}

// pathWithin reports whether p is root itself or a path below it. Drive
// letters are compared without regard to case.
func pathWithin(p, root string) bool {
	if isDrivePath(p) && isDrivePath(root) && strings.EqualFold(p[:1], root[:1]) {
		p = root[:1] + p[1:]
	}
	return p == root || strings.HasPrefix(p, strings.TrimSuffix(root, "/")+"/")
}
