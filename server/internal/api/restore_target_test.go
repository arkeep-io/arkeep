package api

import (
	"strings"
	"testing"
)

func TestNestedRestoreTarget(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		paths    []string
		nested   bool
		contains string
	}{
		{
			name:     "target is the original location of the include path's parent (issue scenario)",
			target:   "/var/lib/docker/volumes/X",
			paths:    []string{"/var/lib/docker/volumes/X/_data"},
			nested:   true,
			contains: "/var/lib/docker/volumes/X/var/lib/docker/volumes/X/_data",
		},
		{name: "target equals the path", target: "/data/app", paths: []string{"/data/app"}, nested: true},
		{name: "trailing slash on target", target: "/data/app/", paths: []string{"/data/app/file.txt"}, nested: true},
		{name: "one of several paths", target: "/srv", paths: []string{"/home/u", "/srv/www"}, nested: true},
		{name: "in-place sentinel", target: "/", paths: []string{"/data/app"}, nested: false},
		{name: "separate folder", target: "/tmp/arkeep-restore", paths: []string{"/data/app"}, nested: false},
		{name: "sibling with a common prefix", target: "/data/app", paths: []string{"/data/application"}, nested: false},
		{name: "target inside the path", target: "/data/app/restore", paths: []string{"/data/app"}, nested: false},
		{name: "no paths", target: "/data", paths: nil, nested: false},
		{name: "docker volume source is skipped", target: "/data", paths: []string{"docker-volume://pg_data"}, nested: false},
		{
			name:     "windows drive paths",
			target:   `C:\Users\me`,
			paths:    []string{`C:\Users\me\Documents`},
			nested:   true,
			contains: "C:/Users/me/C/Users/me/Documents",
		},
		{
			name:     "windows target against restic's form of the include path",
			target:   `C:\Users\me`,
			paths:    []string{"/C/Users/me/Documents"},
			nested:   true,
			contains: "C:/Users/me/C/Users/me/Documents",
		},
		{name: "windows drive letter case", target: `c:\Users\me`, paths: []string{"/C/Users/me/Documents"}, nested: true},
		// Windows paths are case-insensitive, so the whole path is compared
		// without regard to case, not only the drive letter.
		{name: "windows path case", target: `C:\users\ME`, paths: []string{"/C/Users/me/Documents"}, nested: true},
		{name: "windows path case, drive form", target: `c:\USERS\me`, paths: []string{`C:\Users\me`}, nested: true},
		{name: "windows sibling with a common prefix, other case", target: `C:\users\me`, paths: []string{"/C/Users/meg"}, nested: false},
		{name: "unix paths stay case-sensitive", target: "/Data/App", paths: []string{"/data/app"}, nested: false},
		{name: "windows drive root", target: `C:\`, paths: []string{`C:\Users\me`}, nested: true},
		{name: "windows separate folder", target: `C:\restore`, paths: []string{`C:\Users\me`}, nested: false},
		{name: "windows separate folder, restic form", target: `C:\restore`, paths: []string{"/C/Users/me"}, nested: false},
		{name: "windows other drive", target: `D:\data`, paths: []string{"/C/data/x"}, nested: false},
		{name: "unix target keeps /C/ as a unix path", target: "/C", paths: []string{"/C/Users"}, nested: true},
		{name: "relative target is ignored", target: "data", paths: []string{"/data"}, nested: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, nested := nestedRestoreTarget(tt.target, tt.paths)
			if nested != tt.nested {
				t.Fatalf("nestedRestoreTarget(%q, %q) nested = %v, want %v (msg %q)", tt.target, tt.paths, nested, tt.nested, msg)
			}
			if nested && !strings.Contains(msg, "original location") {
				t.Errorf("msg = %q, want it to point to the original location", msg)
			}
			if tt.contains != "" && !strings.Contains(msg, tt.contains) {
				t.Errorf("msg = %q, want it to contain %q", msg, tt.contains)
			}
			// The restore dialog offers no in-place restore on Windows
			// agents, so a Windows target must not be pointed at "/".
			windows := len(tt.target) >= 2 && tt.target[1] == ':'
			if nested && windows && strings.Contains(msg, `target_path "/"`) {
				t.Errorf("msg = %q, want no in-place suggestion for a Windows target", msg)
			}
			if nested && !windows && !strings.Contains(msg, `target_path "/"`) {
				t.Errorf("msg = %q, want it to suggest the in-place restore", msg)
			}
		})
	}
}

func TestRestoreSelection(t *testing.T) {
	if got := restoreSelection([]string{"/a"}, `["/b"]`); len(got) != 1 || got[0] != "/a" {
		t.Errorf("with include paths = %q, want [/a]", got)
	}
	if got := restoreSelection(nil, `["/b","/c"]`); len(got) != 2 || got[1] != "/c" {
		t.Errorf("without include paths = %q, want the sources", got)
	}
	// Policy snapshots store the policy's sources in the GUI's object form.
	policySources := `[{"type":"directory","path":"/srv/app"},{"type":"docker-volume","path":"pg_data"},{"type":"command","path":"pg_dumpall","label":"db"}]`
	if got := restoreSelection(nil, policySources); len(got) != 2 || got[0] != "/srv/app" || got[1] != "docker-volume://pg_data" {
		t.Errorf("policy sources = %q, want [/srv/app docker-volume://pg_data]", got)
	}
	if got := restoreSelection(nil, `not json`); got != nil {
		t.Errorf("malformed sources = %q, want nil", got)
	}
}
