package api

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// restoreSelection returns the paths a restore writes: the include paths when
// the caller selected some, otherwise the snapshot's sources (a JSON array, as
// stored on db.Snapshot.Sources).
func restoreSelection(includePaths []string, sourcesJSON string) []string {
	if len(includePaths) > 0 {
		return includePaths
	}
	var sources []string
	if err := json.Unmarshal([]byte(sourcesJSON), &sources); err != nil {
		return nil
	}
	return sources
}

// nestedRestoreTarget reports whether restoring paths to the folder target
// would nest them inside their own original location. restic recreates each
// path's full absolute path below the target, so restoring /data/app to
// /data/app writes /data/app/data/app and leaves the original files as they
// were, while the job succeeds. The in-place sentinel "/" is never nested.
//
// Paths are compared after turning backslashes into slashes, so Windows paths
// (C:\Users\me) are matched against a target in the same form. Entries that
// are not absolute paths (docker-volume:// and command sources) are skipped.
// When nested is true, msg explains what to do instead.
func nestedRestoreTarget(target string, paths []string) (msg string, nested bool) {
	t, ok := comparablePath(target)
	if !ok || t == "/" {
		return "", false
	}
	for _, p := range paths {
		c, ok := comparablePath(p)
		if !ok || !pathWithin(c, t) {
			continue
		}
		under := c
		if under[0] != '/' { // restic stores C:/Users as /C/Users
			under = "/" + under[:1] + under[2:]
		}
		return fmt.Sprintf(
			"target_path %q is the original location of %q or a folder above it: restoring there would write the files to %q "+
				"and leave the originals unchanged. To put the files back where they were, restore to the original location "+
				"(target_path \"/\"); to keep a separate copy, choose a folder outside %q",
			target, p, path.Join(t, under), target), true
	}
	return "", false
}

// comparablePath returns p with forward slashes, cleaned, when it is an
// absolute Unix path or a Windows drive path.
func comparablePath(p string) (string, bool) {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	isDrive := len(p) >= 3 && p[1] == ':' && p[2] == '/'
	if !strings.HasPrefix(p, "/") && !isDrive {
		return "", false
	}
	return path.Clean(p), true
}

// pathWithin reports whether p is root itself or a path below it.
func pathWithin(p, root string) bool {
	return p == root || strings.HasPrefix(p, strings.TrimSuffix(root, "/")+"/")
}
