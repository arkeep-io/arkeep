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
		{name: "windows separate folder", target: `C:\restore`, paths: []string{`C:\Users\me`}, nested: false},
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
	if got := restoreSelection(nil, `not json`); got != nil {
		t.Errorf("malformed sources = %q, want nil", got)
	}
}
