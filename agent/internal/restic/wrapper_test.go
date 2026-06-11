package restic

import (
	"context"
	"slices"
	"strings"
	"testing"
)

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

func TestBuildCmd_SFTPRepository(t *testing.T) {
	w := &Wrapper{resticBin: "/fake/restic", rcloneBin: "/fake/rclone"}
	dest := Destination{
		Type:     DestSFTP,
		RepoURL:  "sftp:user@backup.example.com:/srv/restic",
		Password: "test-password",
	}

	cmd := w.buildCmd(context.Background(), dest, []string{"snapshots"})

	repoURL := envVar(cmd.Env, "RESTIC_REPOSITORY")
	if repoURL == "" {
		t.Fatal("RESTIC_REPOSITORY not found in cmd.Env")
	}
	if !strings.HasPrefix(repoURL, "sftp:") {
		t.Errorf("RESTIC_REPOSITORY=%q, want prefix 'sftp:'", repoURL)
	}
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

	if err := w.Forget(context.Background(), dest, RetentionPolicy{}); err != nil {
		t.Errorf("Forget with zero policy should be a no-op, got error: %v", err)
	}
}
