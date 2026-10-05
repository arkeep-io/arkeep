package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveHostname(t *testing.T) {
	osName, err := os.Hostname()
	if err != nil || osName == "" {
		osName = "unknown"
	}

	tests := []struct {
		name       string
		override   string
		files      map[string]string // relative to hostRoot
		noHostRoot bool
		want       string
		wantSource string
	}{
		{
			name:       "override wins over host file",
			override:   "  my-server ",
			files:      map[string]string{"etc/hostname": "nas\n"},
			want:       "my-server",
			wantSource: "override",
		},
		{
			name:       "host etc/hostname is read and trimmed",
			files:      map[string]string{"etc/hostname": "  nas-home \n"},
			want:       "nas-home",
			wantSource: "etc/hostname",
		},
		{
			name:       "falls back to etc/HOSTNAME",
			files:      map[string]string{"etc/HOSTNAME": "Tower\n"},
			want:       "Tower",
			wantSource: "etc/HOSTNAME",
		},
		{
			name:       "only the first line is used",
			files:      map[string]string{"etc/hostname": "nas\nignored\n"},
			want:       "nas",
			wantSource: "etc/hostname",
		},
		{
			name:       "empty file falls back to os hostname",
			files:      map[string]string{"etc/hostname": "  \n"},
			want:       osName,
			wantSource: "os",
		},
		{
			name:       "value with inner whitespace is rejected",
			files:      map[string]string{"etc/hostname": "not a hostname\n"},
			want:       osName,
			wantSource: "os",
		},
		{
			name:       "overlong value is rejected",
			files:      map[string]string{"etc/hostname": strings.Repeat("a", maxHostnameLen+1)},
			want:       osName,
			wantSource: "os",
		},
		{
			name:       "missing files fall back to os hostname",
			want:       osName,
			wantSource: "os",
		},
		{
			name:       "no host root skips host files",
			noHostRoot: true,
			want:       osName,
			wantSource: "os",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for rel, content := range tt.files {
				path := filepath.Join(root, rel)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			hostRoot := root
			if tt.noHostRoot {
				hostRoot = ""
			}

			got, source := resolveHostname(tt.override, hostRoot)
			if got != tt.want {
				t.Errorf("hostname = %q, want %q", got, tt.want)
			}
			wantSource := tt.wantSource
			if strings.HasPrefix(wantSource, "etc/") {
				wantSource = filepath.Join(root, wantSource)
			}
			if source != wantSource {
				t.Errorf("source = %q, want %q", source, wantSource)
			}
		})
	}
}
