package restic

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// TestParseResticStats verifies the repository size is extracted from
// `restic stats --json` output, tolerating the progress line restic prints
// alongside the JSON object on stdout.
func TestParseResticStats(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantSize  uint64
		wantFound bool
	}{
		{
			name:      "json only",
			input:     `{"total_size":3334,"total_uncompressed_size":6067,"total_blob_count":13,"snapshots_count":1}`,
			wantSize:  3334,
			wantFound: true,
		},
		{
			name: "progress line before json",
			input: "[0:00] 100.00%  1 / 1 snapshots, 13 blobs, 3.256 KiB\n" +
				`{"total_size":987654,"snapshots_count":2}`,
			wantSize:  987654,
			wantFound: true,
		},
		{
			name:      "no json object",
			input:     "[0:00] 100.00%  0 snapshots\n",
			wantSize:  0,
			wantFound: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found, err := parseResticStats(strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if found != tt.wantFound {
				t.Errorf("found = %v, want %v", found, tt.wantFound)
			}
			if got.TotalSize != tt.wantSize {
				t.Errorf("TotalSize = %d, want %d", got.TotalSize, tt.wantSize)
			}
		})
	}
}

// envVar extracts the value of a "KEY=value" entry from an environment slice.
// Returns empty string if the key is not present.
func envVar(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix)
		}
	}
	return ""
}

func TestBuildCmd_S3Repository(t *testing.T) {
	w := &Wrapper{resticBin: "/fake/restic", rcloneBin: "/fake/rclone"}
	dest := Destination{
		Type:     DestS3,
		RepoURL:  "s3:s3.amazonaws.com/my-bucket",
		Password: "test-password",
		Env: map[string]string{
			"AWS_ACCESS_KEY_ID":     "AKIAIOSFODNN7EXAMPLE",
			"AWS_SECRET_ACCESS_KEY": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
	}

	cmd := w.buildCmd(context.Background(), dest, []string{"snapshots"})

	repoURL := envVar(cmd.Env, "RESTIC_REPOSITORY")
	if repoURL == "" {
		t.Fatal("RESTIC_REPOSITORY not found in cmd.Env")
	}
	if !strings.HasPrefix(repoURL, "s3:") {
		t.Errorf("RESTIC_REPOSITORY=%q, want prefix 's3:'", repoURL)
	}
}

func TestBuildBackupArgs_Windows(t *testing.T) {
	opts := BackupOptions{
		Tags:    []string{"weekly"},
		Sources: []string{`C:\Users`},
	}
	args := buildBackupArgs(opts, "windows")

	if !slices.Contains(args, "--use-fs-snapshot") {
		t.Errorf("expected --use-fs-snapshot in args on windows, got %v", args)
	}
}

func TestBuildBackupArgs_Linux(t *testing.T) {
	opts := BackupOptions{
		Tags:    []string{"weekly"},
		Sources: []string{"/home"},
	}
	args := buildBackupArgs(opts, "linux")

	if slices.Contains(args, "--use-fs-snapshot") {
		t.Errorf("unexpected --use-fs-snapshot in args on linux, got %v", args)
	}
}

// SFTP destinations are routed through rclone (repo URL "rclone:..."), so
// buildCmd must point restic at the embedded rclone binary even though the
// destination type is sftp, not rclone.
func TestBuildCmd_SFTPRepository(t *testing.T) {
	w := &Wrapper{resticBin: "/fake/restic", rcloneBin: "/fake/rclone"}
	dest := Destination{
		Type:    DestSFTP,
		RepoURL: "rclone:arkeepsftp:/srv/restic",
		Env: map[string]string{
			"RCLONE_CONFIG_ARKEEPSFTP_TYPE": "sftp",
			"RCLONE_CONFIG_ARKEEPSFTP_HOST": "backup.example.com",
		},
	}

	cmd := w.buildCmd(context.Background(), dest, []string{"snapshots"})

	if !hasRcloneProgramOpt(cmd.Args, "/fake/rclone") {
		t.Errorf("expected -o rclone.program=/fake/rclone in args, got %v", cmd.Args)
	}
	if got := envVar(cmd.Env, "RCLONE_CONFIG_ARKEEPSFTP_HOST"); got != "backup.example.com" {
		t.Errorf("rclone remote host env not propagated, got %q", got)
	}
}

// Pure local/s3 destinations must NOT get the rclone.program option.
func TestBuildCmd_NonRcloneNoRcloneProgram(t *testing.T) {
	w := &Wrapper{resticBin: "/fake/restic", rcloneBin: "/fake/rclone"}
	dest := Destination{Type: DestLocal, RepoURL: "/mnt/backups"}

	cmd := w.buildCmd(context.Background(), dest, []string{"snapshots"})

	if hasRcloneProgramOpt(cmd.Args, "/fake/rclone") {
		t.Errorf("rclone.program option should be unset for local dest, got %v", cmd.Args)
	}
}

// hasRcloneProgramOpt reports whether args contains the consecutive pair
// "-o" "rclone.program=<bin>".
func hasRcloneProgramOpt(args []string, bin string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-o" && args[i+1] == "rclone.program="+bin {
			return true
		}
	}
	return false
}

func TestRetentionPolicy_IsEnabled(t *testing.T) {
	tests := []struct {
		name   string
		policy RetentionPolicy
		want   bool
	}{
		{"all zero", RetentionPolicy{}, false},
		{"last only", RetentionPolicy{Last: 5}, true},
		{"hourly only", RetentionPolicy{Hourly: 24}, true},
		{"daily only", RetentionPolicy{Daily: 7}, true},
		{"weekly only", RetentionPolicy{Weekly: 4}, true},
		{"monthly only", RetentionPolicy{Monthly: 6}, true},
		{"yearly only", RetentionPolicy{Yearly: 1}, true},
		{"all non-zero", RetentionPolicy{Last: 1, Hourly: 2, Daily: 3, Weekly: 4, Monthly: 5, Yearly: 6}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.policy.IsEnabled(); got != tt.want {
				t.Errorf("IsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestForget_ZeroPolicy_Skips(t *testing.T) {
	// A non-existent binary ensures run() would fail if called.
	w := &Wrapper{resticBin: "/nonexistent/restic", rcloneBin: "/nonexistent/rclone"}
	dest := Destination{Type: DestLocal, RepoURL: "/tmp/repo", Password: "pw"}

	if err := w.Forget(context.Background(), dest, RetentionPolicy{}, []string{"policy:abc"}); err != nil {
		t.Errorf("Forget with zero policy should be a no-op, got error: %v", err)
	}
}

// TestForget_NoTags_Fails verifies that Forget refuses to run without tags.
// An unscoped forget considers every snapshot in the repository, so a
// destination shared by two policies would have both pruned by whichever ran
// last. Pruning nothing is recoverable; pruning another policy's snapshots is
// not — so this must be an error, never a fallback to the unscoped command.
func TestForget_NoTags_Fails(t *testing.T) {
	w := &Wrapper{resticBin: "/nonexistent/restic", rcloneBin: "/nonexistent/rclone"}
	dest := Destination{Type: DestLocal, RepoURL: "/tmp/repo", Password: "pw"}

	err := w.Forget(context.Background(), dest, RetentionPolicy{Daily: 7}, nil)
	if err == nil {
		t.Fatal("Forget without tags should fail, got nil error")
	}
	if !strings.Contains(err.Error(), "without tags") {
		t.Errorf("error should explain the missing tags, got: %v", err)
	}
}

// TestBuildForgetArgs verifies that retention is always scoped by tag and that
// only the configured keep rules are passed through.
func TestBuildForgetArgs(t *testing.T) {
	tests := []struct {
		name    string
		policy  RetentionPolicy
		tags    []string
		want    []string
		wantErr bool
	}{
		{
			name:   "single keep rule is tag-scoped",
			policy: RetentionPolicy{Daily: 7},
			tags:   []string{"policy:11111111-2222-3333-4444-555555555555"},
			want: []string{
				"forget", "--prune", "--json",
				"--tag", "policy:11111111-2222-3333-4444-555555555555",
				"--keep-daily", "7",
			},
		},
		{
			name:   "all keep rules in order",
			policy: RetentionPolicy{Last: 1, Hourly: 2, Daily: 3, Weekly: 4, Monthly: 5, Yearly: 6},
			tags:   []string{"policy:abc"},
			want: []string{
				"forget", "--prune", "--json", "--tag", "policy:abc",
				"--keep-last", "1", "--keep-hourly", "2", "--keep-daily", "3",
				"--keep-weekly", "4", "--keep-monthly", "5", "--keep-yearly", "6",
			},
		},
		{
			name:   "zero keep rules are omitted",
			policy: RetentionPolicy{Weekly: 4},
			tags:   []string{"policy:abc"},
			want: []string{
				"forget", "--prune", "--json", "--tag", "policy:abc",
				"--keep-weekly", "4",
			},
		},
		{
			name:   "each tag gets its own flag",
			policy: RetentionPolicy{Last: 1},
			tags:   []string{"policy:abc", "env:prod"},
			want: []string{
				"forget", "--prune", "--json",
				"--tag", "policy:abc", "--tag", "env:prod",
				"--keep-last", "1",
			},
		},
		{
			name:    "no tags is rejected",
			policy:  RetentionPolicy{Daily: 7},
			tags:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildForgetArgs(tt.policy, tt.tags)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("buildForgetArgs() = %v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildForgetArgs() unexpected error: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("buildForgetArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}
