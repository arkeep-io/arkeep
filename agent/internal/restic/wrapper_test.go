package restic

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.uber.org/zap"
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
	assertSourcesAfterEndOfOptions(t, args, opts.Sources)
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
	assertSourcesAfterEndOfOptions(t, args, opts.Sources)
}

// TestBuildBackupArgs_Host verifies the snapshot hostname is pinned with
// --host when set (a Dockerized agent would otherwise stamp its container ID,
// #284), left to restic when empty, and always kept before "--".
func TestBuildBackupArgs_Host(t *testing.T) {
	opts := BackupOptions{Sources: []string{"/data"}, Host: "nas-home"}
	args := buildBackupArgs(opts, "linux")
	idx := slices.Index(args, "--host")
	if idx == -1 || idx+1 >= len(args) || args[idx+1] != "nas-home" {
		t.Fatalf("expected --host nas-home in args, got %v", args)
	}
	if idx > slices.Index(args, "--") {
		t.Errorf("--host must come before \"--\", got %v", args)
	}
	assertSourcesAfterEndOfOptions(t, args, opts.Sources)

	opts.Host = ""
	if args := buildBackupArgs(opts, "linux"); slices.Contains(args, "--host") {
		t.Errorf("unexpected --host with empty Host, got %v", args)
	}
}

func TestBuildStdinBackupArgs_Host(t *testing.T) {
	opts := StdinBackupOptions{Command: "pg_dump mydb", Filename: "pgdump", Host: "nas-home"}
	args := buildStdinBackupArgs(opts, "linux")

	want := []string{
		"backup", "--json", "--stdin-from-command",
		"--stdin-filename", "pgdump",
		"--host", "nas-home",
		"--", "/bin/sh", "-c", "pg_dump mydb",
	}
	if !slices.Equal(args, want) {
		t.Errorf("buildStdinBackupArgs() = %v, want %v", args, want)
	}
}

// assertSourcesAfterEndOfOptions verifies that wantSources are exactly the
// tail of args, immediately after a "--" end-of-options marker. This is the
// property that prevents a source beginning with "-" (e.g.
// "--password-command=...") from ever being parsed by restic as a flag.
func assertSourcesAfterEndOfOptions(t *testing.T, args []string, wantSources []string) {
	t.Helper()
	idx := slices.Index(args, "--")
	if idx == -1 {
		t.Fatalf("expected a \"--\" end-of-options marker in args, got %v", args)
	}
	got := args[idx+1:]
	if !slices.Equal(got, wantSources) {
		t.Errorf("args after \"--\" = %v, want %v", got, wantSources)
	}
}

// TestBuildBackupArgs_SourcesAfterEndOfOptions is a regression test for
// GHSA-263g-c333-jcjq / GHSA-75rg-4ppf-pq7g: a source entry that looks like a
// restic flag must still end up strictly after "--", proving it cannot be
// reinterpreted by restic as e.g. --password-command, even if validation
// upstream were ever bypassed.
func TestBuildBackupArgs_SourcesAfterEndOfOptions(t *testing.T) {
	opts := BackupOptions{
		Tags:    []string{"weekly"},
		Sources: []string{"--password-command=touch /tmp/pwned", "/data"},
	}
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			args := buildBackupArgs(opts, goos)
			assertSourcesAfterEndOfOptions(t, args, opts.Sources)
		})
	}
}

// TestBuildRestoreArgs_SnapshotIDAfterEndOfOptions is a defense-in-depth
// regression test: snapshotID must be the final element, after "--", so it
// can never be parsed as a restic flag even though today's callers only ever
// pass restic-generated snapshot hashes.
func TestBuildRestoreArgs_SnapshotIDAfterEndOfOptions(t *testing.T) {
	args := buildRestoreArgs("--password-command=touch /tmp/pwned", "/restore/target", []string{"/inc"}, []string{"/exc"}, "linux")

	idx := slices.Index(args, "--")
	if idx == -1 {
		t.Fatalf("expected a \"--\" end-of-options marker in args, got %v", args)
	}
	if idx != len(args)-2 {
		t.Errorf("expected \"--\" immediately before the final element, got %v", args)
	}
	if got := args[len(args)-1]; got != "--password-command=touch /tmp/pwned" {
		t.Errorf("snapshotID = %q, want it as the final positional arg", got)
	}
}

func TestBuildStdinBackupArgs_Linux(t *testing.T) {
	opts := StdinBackupOptions{
		Command:  "pg_dump -U postgres mydb | gzip",
		Filename: "pgdump",
		Tags:     []string{"policy:abc:command:pgdump"},
	}
	args := buildStdinBackupArgs(opts, "linux")

	want := []string{
		"backup", "--json", "--stdin-from-command",
		"--stdin-filename", "pgdump",
		"--tag", "policy:abc:command:pgdump",
		"--", "/bin/sh", "-c", "pg_dump -U postgres mydb | gzip",
	}
	if !slices.Equal(args, want) {
		t.Errorf("buildStdinBackupArgs() = %v, want %v", args, want)
	}
	if slices.Contains(args, "--use-fs-snapshot") {
		t.Error("unexpected --use-fs-snapshot: there is no filesystem to snapshot for a stdin backup")
	}
}

func TestBuildStdinBackupArgs_Windows(t *testing.T) {
	opts := StdinBackupOptions{
		Command:  "pg_dump mydb",
		Filename: "pgdump",
		Tags:     []string{"policy:abc:command:pgdump"},
	}
	args := buildStdinBackupArgs(opts, "windows")

	want := []string{
		"backup", "--json", "--stdin-from-command",
		"--stdin-filename", "pgdump",
		"--tag", "policy:abc:command:pgdump",
		"--", "cmd", "/C", "pg_dump mydb",
	}
	if !slices.Equal(args, want) {
		t.Errorf("buildStdinBackupArgs() = %v, want %v", args, want)
	}
}

// TestBuildStdinBackupArgs_CommandAfterEndOfOptions is a regression test
// mirroring TestBuildBackupArgs_SourcesAfterEndOfOptions: a command
// beginning with "-" must still land strictly after "--", so it can never be
// reinterpreted by restic as one of its own flags (e.g. --password-command).
func TestBuildStdinBackupArgs_CommandAfterEndOfOptions(t *testing.T) {
	opts := StdinBackupOptions{Command: "--password-command=touch /tmp/pwned", Filename: "x"}
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			args := buildStdinBackupArgs(opts, goos)
			idx := slices.Index(args, "--")
			if idx == -1 {
				t.Fatalf("expected a \"--\" end-of-options marker in args, got %v", args)
			}
			if got := args[len(args)-1]; got != opts.Command {
				t.Errorf("command = %q, want it as the final positional arg", got)
			}
		})
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

// Destination env vars outside the backend allowlist must never reach restic:
// RESTIC_PASSWORD_COMMAND runs a command, LD_PRELOAD loads a library, and
// RESTIC_REPOSITORY would redirect the repository.
func TestBuildCmd_DropsEnvOutsideAllowlist(t *testing.T) {
	w := &Wrapper{resticBin: "/fake/restic", rcloneBin: "/fake/rclone", logger: zap.NewNop()}
	dest := Destination{
		Type:     DestS3,
		RepoURL:  "s3:s3.example.com/bucket",
		Password: "repo-pass",
		Env: map[string]string{
			"AWS_ACCESS_KEY_ID":              "AKIA",
			"aws_secret_access_key":          "secret",
			"RESTIC_PASSWORD_COMMAND":        "sh -c id",
			"LD_PRELOAD":                     "/tmp/x.so",
			"RESTIC_REPOSITORY":              "/tmp/elsewhere",
			"RCLONE_CONFIG_ARKEEPSFTP_SSH":   "id",
			"RCLONE_CONFIG_OTHERREMOTE_TYPE": "local",
			// The server never picks a file on the agent: only the content.
			"RCLONE_CONFIG_ARKEEPSFTP_KNOWN_HOSTS_FILE": "/etc/shadow",
		},
	}

	cmd := w.buildCmd(context.Background(), dest, []string{"snapshots"})

	if got := envVar(cmd.Env, "AWS_ACCESS_KEY_ID"); got != "AKIA" {
		t.Errorf("AWS_ACCESS_KEY_ID = %q, want AKIA", got)
	}
	if got := envVar(cmd.Env, "aws_secret_access_key"); got != "secret" {
		t.Errorf("allowlist must match case-insensitively, got %q", got)
	}
	for _, k := range []string{"RESTIC_PASSWORD_COMMAND", "LD_PRELOAD", "RCLONE_CONFIG_ARKEEPSFTP_SSH", "RCLONE_CONFIG_ARKEEPSFTP_KNOWN_HOSTS_FILE", "RCLONE_CONFIG_OTHERREMOTE_TYPE"} {
		for _, kv := range cmd.Env {
			if strings.HasPrefix(kv, k+"=") && os.Getenv(k) == "" {
				t.Errorf("%s reached the restic environment", k)
			}
		}
	}
	if got := envVar(cmd.Env, "RESTIC_REPOSITORY"); got != "s3:s3.example.com/bucket" {
		t.Errorf("RESTIC_REPOSITORY = %q, must not be overridden by destination env", got)
	}
}

// TestBuildCmd_SFTPKnownHosts: the known_hosts content the server sends is
// written to a private file and only its path reaches rclone, so a pinned SFTP
// host key is checked (SEC-31).
func TestBuildCmd_SFTPKnownHosts(t *testing.T) {
	w := &Wrapper{resticBin: "/fake/restic", rcloneBin: "/fake/rclone", logger: zap.NewNop()}
	const content = "[h.example.com]:2222 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample\n"
	dest := Destination{
		Type:    DestSFTP,
		RepoURL: "rclone:arkeepsftp:/srv/restic",
		Env: map[string]string{
			"RCLONE_CONFIG_ARKEEPSFTP_TYPE": "sftp",
			"ARKEEP_SFTP_KNOWN_HOSTS":       content,
		},
	}

	cmd := w.buildCmd(context.Background(), dest, []string{"snapshots"})

	path := envVar(cmd.Env, "RCLONE_CONFIG_ARKEEPSFTP_KNOWN_HOSTS_FILE")
	if path == "" {
		t.Fatal("KNOWN_HOSTS_FILE not set")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != content {
		t.Errorf("known_hosts file = %q (%v), want the content sent by the server", got, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Dir(path))
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("known_hosts directory mode = %v (%v), want private 0700", info.Mode().Perm(), err)
		}
	}
	if v := envVar(cmd.Env, "ARKEEP_SFTP_KNOWN_HOSTS"); v != "" {
		t.Errorf("ARKEEP_SFTP_KNOWN_HOSTS reached the restic environment")
	}

	// The same content maps to the same file.
	again := w.buildCmd(context.Background(), dest, []string{"snapshots"})
	if p := envVar(again.Env, "RCLONE_CONFIG_ARKEEPSFTP_KNOWN_HOSTS_FILE"); p != path {
		t.Errorf("second command uses %q, want %q", p, path)
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
				"forget", "--prune", "--json", "--group-by", "paths,tags",
				"--tag", "policy:11111111-2222-3333-4444-555555555555",
				"--keep-daily", "7",
			},
		},
		{
			name:   "all keep rules in order",
			policy: RetentionPolicy{Last: 1, Hourly: 2, Daily: 3, Weekly: 4, Monthly: 5, Yearly: 6},
			tags:   []string{"policy:abc"},
			want: []string{
				"forget", "--prune", "--json", "--group-by", "paths,tags", "--tag", "policy:abc",
				"--keep-last", "1", "--keep-hourly", "2", "--keep-daily", "3",
				"--keep-weekly", "4", "--keep-monthly", "5", "--keep-yearly", "6",
			},
		},
		{
			name:   "zero keep rules are omitted",
			policy: RetentionPolicy{Weekly: 4},
			tags:   []string{"policy:abc"},
			want: []string{
				"forget", "--prune", "--json", "--group-by", "paths,tags", "--tag", "policy:abc",
				"--keep-weekly", "4",
			},
		},
		{
			name:   "each tag gets its own flag",
			policy: RetentionPolicy{Last: 1},
			tags:   []string{"policy:abc", "env:prod"},
			want: []string{
				"forget", "--prune", "--json", "--group-by", "paths,tags",
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
		{
			// A command source's own retention pool (see buildStdinBackupArgs):
			// it must be the ONLY tag, never combined with the bare
			// "policy:<id>" tag also used by the regular pool, or forgetting
			// one pool would sweep the other's snapshots too.
			name:   "command source retention tag is passed through verbatim",
			policy: RetentionPolicy{Last: 7},
			tags:   []string{"policy:abc:command:pgdump"},
			want: []string{
				"forget", "--prune", "--json", "--group-by", "paths,tags",
				"--tag", "policy:abc:command:pgdump",
				"--keep-last", "7",
			},
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

// TestBuildCmd_GracefulCancelSendsSIGINT verifies that cancelling the context
// passed to buildCmd sends SIGINT to the child process instead of the Go
// default (an immediate SIGKILL). This is what gives restic a chance to run
// its own shutdown handler and release its repository lock — without it, any
// cancellation (job cancel, agent shutdown, systemd restart) that lands
// mid-operation orphans an exclusive lock that blocks every future run.
func TestBuildCmd_GracefulCancelSendsSIGINT(t *testing.T) {
	// /bin/sleep installs no signal handler of its own, so its termination
	// signal directly reflects what buildCmd's cmd.Cancel actually sent —
	// no shell/trap semantics in the way (POSIX shells only run pending traps
	// once their current foreground child exits, which would make a
	// shell-script stand-in useless for testing prompt signal delivery).
	w := &Wrapper{resticBin: "/bin/sleep", rcloneBin: "/fake/rclone", logger: zap.NewNop()}
	dest := Destination{Type: DestLocal, RepoURL: "/tmp", Password: "pw"}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := w.buildCmd(ctx, dest, []string{"5"})

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start /bin/sleep: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	cancel()

	waitErr := cmd.Wait()
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) {
		t.Fatalf("Wait() error = %v, want *exec.ExitError", waitErr)
	}
	ws, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("could not read a Unix wait status from %v", exitErr)
	}
	if !ws.Signaled() || ws.Signal() != syscall.SIGINT {
		t.Errorf("process terminated by signal %v (signaled=%v), want SIGINT — "+
			"the default (Go stdlib) behavior of an immediate SIGKILL would never "+
			"give restic a chance to release its repository lock", ws.Signal(), ws.Signaled())
	}
}

// writeFakeResticScript creates a shell script standing in for the restic
// binary. It handles two commands: "unlock", which always succeeds and
// records that it ran, and any other command ("the real command"), which
// fails with restic's lock-conflict exit code (11) for the first failTimes
// invocations and succeeds afterwards. Invocation counts are tracked via
// files so the test can assert exactly how many times each command ran.
func writeFakeResticScript(t *testing.T, dir string, failTimes int) (script, counterFile, unlockMarker string) {
	t.Helper()
	script = filepath.Join(dir, "fake-restic.sh")
	counterFile = filepath.Join(dir, "count")
	unlockMarker = filepath.Join(dir, "unlocked")

	body := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "unlock" ]; then
  echo ran >> %q
  exit 0
fi
count=0
if [ -f %q ]; then
  count=$(cat %q)
fi
count=$((count+1))
echo "$count" > %q
if [ "$count" -le %d ]; then
  echo "unable to create lock in backend: repository is already locked exclusively" >&2
  exit 11
fi
exit 0
`, unlockMarker, counterFile, counterFile, counterFile, failTimes)

	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, counterFile, unlockMarker
}

func readCounter(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &n); err != nil {
		t.Fatalf("failed to parse counter file %q = %q: %v", path, data, err)
	}
	return n
}

// TestRun_RetriesOnceAfterUnlockOnLockConflict is the end-to-end regression
// for issue #252: a command that fails once with restic's lock-conflict exit
// code must trigger `restic unlock` and a single retry, succeeding overall —
// instead of failing identically forever the way Arkeep did before this fix.
func TestRun_RetriesOnceAfterUnlockOnLockConflict(t *testing.T) {
	dir := t.TempDir()
	script, counterFile, unlockMarker := writeFakeResticScript(t, dir, 1)
	w := &Wrapper{resticBin: script, rcloneBin: "/fake/rclone", logger: zap.NewNop()}
	dest := Destination{Type: DestLocal, RepoURL: dir, Password: "pw"}

	if err := w.run(context.Background(), dest, []string{"forget"}); err != nil {
		t.Fatalf("run() = %v, want nil (should recover after unlock)", err)
	}

	if got := readCounter(t, counterFile); got != 2 {
		t.Errorf("real command invoked %d times, want 2 (one failure + one retry)", got)
	}
	if _, err := os.Stat(unlockMarker); err != nil {
		t.Error("restic unlock was not invoked")
	}
}

// TestRun_DoesNotRetryOnOtherErrors verifies that only the specific
// lock-conflict exit code (11) triggers the unlock-and-retry path — any other
// failure is returned immediately, with no unlock attempt.
func TestRun_DoesNotRetryOnOtherErrors(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-restic.sh")
	unlockMarker := filepath.Join(dir, "unlocked")
	body := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"unlock\" ]; then echo ran >> %q; exit 0; fi\nexit 1\n", unlockMarker)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	w := &Wrapper{resticBin: script, rcloneBin: "/fake/rclone", logger: zap.NewNop()}
	dest := Destination{Type: DestLocal, RepoURL: dir, Password: "pw"}

	if err := w.run(context.Background(), dest, []string{"forget"}); err == nil {
		t.Fatal("run() = nil, want an error for a non-lock-conflict failure")
	}
	if _, err := os.Stat(unlockMarker); err == nil {
		t.Error("restic unlock should not be invoked for a non-lock-conflict failure")
	}
}

// TestRun_FailsIfSecondAttemptAlsoLocked verifies there is no unbounded retry
// loop: if the retry ALSO hits a lock conflict (e.g. another operation is
// genuinely still running), the error is propagated after exactly one retry.
func TestRun_FailsIfSecondAttemptAlsoLocked(t *testing.T) {
	dir := t.TempDir()
	script, counterFile, unlockMarker := writeFakeResticScript(t, dir, 2)
	w := &Wrapper{resticBin: script, rcloneBin: "/fake/rclone", logger: zap.NewNop()}
	dest := Destination{Type: DestLocal, RepoURL: dir, Password: "pw"}

	if err := w.run(context.Background(), dest, []string{"forget"}); err == nil {
		t.Fatal("run() = nil, want an error when the retry also hits a lock conflict")
	}

	if got := readCounter(t, counterFile); got != 2 {
		t.Errorf("real command invoked %d times, want 2 (one failure + exactly one retry, no more)", got)
	}
	if _, err := os.Stat(unlockMarker); err != nil {
		t.Error("restic unlock should have been attempted once")
	}
}

// TestBuildDumpArgs checks the dump arguments for a file and for a directory
// archive, and that the "--" marker keeps a path beginning with "-" from being
// read as a flag.
func TestBuildDumpArgs(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		archive bool
		want    []string
	}{
		{"file", "/etc/hosts", false, []string{"dump", "--", "abc123", "/etc/hosts"}},
		{"directory as zip", "/etc", true, []string{"dump", "--archive", "zip", "--", "abc123", "/etc"}},
		{"path looking like a flag", "--password-command=id", false, []string{"dump", "--", "abc123", "--password-command=id"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildDumpArgs("abc123", tt.path, tt.archive)
			if !slices.Equal(got, tt.want) {
				t.Errorf("buildDumpArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildCheckArgs(t *testing.T) {
	tests := []struct {
		name    string
		opts    CheckOptions
		want    []string
		wantErr bool
	}{
		{"structure", CheckOptions{Mode: CheckModeStructure}, []string{"check", "--json"}, false},
		{"subset", CheckOptions{Mode: CheckModeSubset, SubsetPercent: 5}, []string{"check", "--json", "--read-data-subset=5%"}, false},
		{"subset 100", CheckOptions{Mode: CheckModeSubset, SubsetPercent: 100}, []string{"check", "--json", "--read-data-subset=100%"}, false},
		{"subset 0", CheckOptions{Mode: CheckModeSubset, SubsetPercent: 0}, nil, true},
		{"subset 101", CheckOptions{Mode: CheckModeSubset, SubsetPercent: 101}, nil, true},
		{"full", CheckOptions{Mode: CheckModeFull}, []string{"check", "--json", "--read-data"}, false},
		{"unknown mode", CheckOptions{Mode: "deep"}, nil, true},
		{"empty mode", CheckOptions{}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildCheckArgs(tt.opts)
			if (err != nil) != tt.wantErr {
				t.Fatalf("buildCheckArgs(%+v) error = %v, wantErr %v", tt.opts, err, tt.wantErr)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("buildCheckArgs(%+v) = %v, want %v", tt.opts, got, tt.want)
			}
		})
	}
}

// TestParseCheckOutput uses output captured from restic 0.19.1 against a
// repository with one corrupted pack file.
func TestParseCheckOutput(t *testing.T) {
	stdout := `{"message_type":"summary","num_errors":1,"broken_packs":["8beb"],"suggest_repair_index":false,"suggest_prune":false}` + "\n"
	stderr := `{"message_type":"error","message":"pack 8beb contains 2 errors: [blob a83b: ciphertext verification failed]\n"}
{"message_type":"error","message":"\nThe repository contains damaged pack files.\n\n"}
{"message_type":"error","message":"restic repair packs 8beb\nrestic repair snapshots --forget\n\n"}
{"message_type":"exit_error","code":1,"message":"Fatal: repository contains errors"}
`
	got := parseCheckOutput(stdout, stderr)
	if got.NumErrors != 1 || !slices.Equal(got.BrokenPacks, []string{"8beb"}) {
		t.Errorf("summary = (%d errors, packs %v), want (1, [8beb])", got.NumErrors, got.BrokenPacks)
	}
	wantMessages := []string{
		"pack 8beb contains 2 errors: [blob a83b: ciphertext verification failed]",
		"The repository contains damaged pack files.",
		"restic repair packs 8beb\nrestic repair snapshots --forget",
		"Fatal: repository contains errors",
	}
	if !slices.Equal(got.Messages, wantMessages) {
		t.Errorf("Messages = %q, want %q", got.Messages, wantMessages)
	}

	clean := parseCheckOutput(`{"message_type":"summary","num_errors":0,"broken_packs":null}`, "")
	if clean.NumErrors != 0 || len(clean.BrokenPacks) != 0 || len(clean.Messages) != 0 {
		t.Errorf("clean check = %+v, want an empty result", clean)
	}

	plain := parseCheckOutput("", "Fatal: wrong password or no key found\n")
	if !slices.Equal(plain.Messages, []string{"Fatal: wrong password or no key found"}) {
		t.Errorf("non-JSON stderr Messages = %q, want the raw line", plain.Messages)
	}
}

// TestEmptyRestoreSummary uses lines printed by restic 0.19.1: a selection
// that matches nothing ends with a summary without total_files.
func TestEmptyRestoreSummary(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantEmpty bool
		wantOK    bool
	}{
		{"nothing matched", `{"message_type":"summary"}`, true, true},
		{"one file", `{"message_type":"summary","total_files":1,"files_restored":9,"total_bytes":3,"bytes_restored":3}`, false, true},
		{"empty directory", `{"message_type":"summary","total_files":7,"files_restored":7}`, false, true},
		{"progress line", `{"message_type":"status","percent_done":0.5,"total_files":4}`, false, false},
		{"not json", `restoring <Snapshot 1a2b3c4d>`, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			empty, ok := emptyRestoreSummary([]byte(tt.line))
			if empty != tt.wantEmpty || ok != tt.wantOK {
				t.Errorf("emptyRestoreSummary(%s) = (%v, %v), want (%v, %v)", tt.line, empty, ok, tt.wantEmpty, tt.wantOK)
			}
		})
	}
}

// writeMissingRepoScript creates a fake restic that records each subcommand
// it is run with in a log file, succeeds for "init" and exits with restic's
// "repository does not exist" status (10) for anything else.
func writeMissingRepoScript(t *testing.T, dir string) (script, calls string) {
	t.Helper()
	script = filepath.Join(dir, "fake-restic.sh")
	calls = filepath.Join(dir, "calls")
	body := fmt.Sprintf(`#!/bin/sh
for a in "$@"; do
  case "$a" in -*) ;; *) echo "$a" >> %q; break ;; esac
done
[ "$1" = "init" ] && exit 0
echo "Fatal: repository does not exist: unable to open config file" >&2
exit 10
`, calls)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, calls
}

// TestBackup_RepositoryThatMustExist covers a repository that has gone missing
// (an unmounted disk): when it is known to exist the backup must neither run
// `restic init` nor report a plain restic failure, but ErrRepositoryNotFound.
// A repository not yet known to exist is still created first.
func TestBackup_RepositoryThatMustExist(t *testing.T) {
	backups := map[string]func(w *Wrapper, dest Destination) error{
		"Backup": func(w *Wrapper, dest Destination) error {
			_, err := w.Backup(context.Background(), dest, BackupOptions{Sources: []string{"/data"}}, nil)
			return err
		},
		"BackupFromCommand": func(w *Wrapper, dest Destination) error {
			_, err := w.BackupFromCommand(context.Background(), dest, StdinBackupOptions{Command: "true", Filename: "db"}, nil)
			return err
		},
	}
	for name, backup := range backups {
		t.Run(name, func(t *testing.T) {
			for _, mustExist := range []bool{true, false} {
				dir := t.TempDir()
				script, calls := writeMissingRepoScript(t, dir)
				w := &Wrapper{resticBin: script, rcloneBin: "/fake/rclone", logger: zap.NewNop()}
				dest := Destination{Type: DestLocal, RepoURL: filepath.Join(dir, "repo"), Password: "pw", MustExist: mustExist}

				err := backup(w, dest)
				if err == nil {
					t.Fatalf("MustExist=%v: backup succeeded, want an error", mustExist)
				}
				data, _ := os.ReadFile(calls)
				ranInit := strings.HasPrefix(string(data), "init\n")
				if ranInit == mustExist {
					t.Errorf("MustExist=%v: restic ran %q, want init only when the repository is not known to exist", mustExist, data)
				}
				if got := errors.Is(err, ErrRepositoryNotFound); got != mustExist {
					t.Errorf("MustExist=%v: errors.Is(err, ErrRepositoryNotFound) = %v; err = %v", mustExist, got, err)
				}
				if mustExist && !strings.Contains(err.Error(), dest.RepoURL) {
					t.Errorf("error %q does not name the repository location", err)
				}
			}
		})
	}
}

func TestErrorMessage(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   string
	}{
		{
			name:   "json exit_error",
			stderr: `{"message_type":"exit_error","code":12,"message":"Fatal: wrong password or no key found\n"}` + "\n",
			want:   "wrong password or no key found",
		},
		{
			name:   "plain text",
			stderr: "\nFatal: unable to open repo at /x: connection refused\n",
			want:   "unable to open repo at /x: connection refused",
		},
		{
			name: "per-file errors and exit_error",
			stderr: `{"message_type":"error","error":{"message":"open /a: permission denied"},"during":"restore","item":"/a"}` + "\n" +
				`{"message_type":"error","error":{"message":"open /b: permission denied"},"during":"restore","item":"/b"}` + "\n" +
				`{"message_type":"exit_error","code":1,"message":"Fatal: There were 2 errors\n"}` + "\n",
			want: "There were 2 errors: open /a: permission denied; open /b: permission denied",
		},
		{
			name: "more per-file errors than shown",
			stderr: `{"message_type":"error","error":{"message":"e1"},"item":"/1"}` + "\n" +
				`{"message_type":"error","error":{"message":"e2"},"item":"/2"}` + "\n" +
				`{"message_type":"error","error":{"message":"e3"},"item":"/3"}` + "\n" +
				`{"message_type":"error","error":{"message":"e4"},"item":"/4"}` + "\n" +
				`{"message_type":"exit_error","code":1,"message":"Fatal: There were 4 errors\n"}` + "\n",
			want: "There were 4 errors: e1; e2; e3; and 1 more",
		},
		{
			name:   "json without exit_error falls back to stderr",
			stderr: `{"message_type":"status","percent_done":0.5}` + "\nsomething broke\n",
			want:   `{"message_type":"status","percent_done":0.5}` + "\nsomething broke",
		},
		{
			name:   "empty",
			stderr: " \n",
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errorMessage(tt.stderr); got != tt.want {
				t.Errorf("errorMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRun_ReadableErrorKeepsExitStatus checks the error of a failed restic
// run: restic's own message, then the exit status, still unwrappable to the
// *exec.ExitError (lock-conflict detection and exit-code checks rely on it).
func TestRun_ReadableErrorKeepsExitStatus(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-restic.sh")
	body := "#!/bin/sh\nif [ \"$1\" = \"unlock\" ]; then exit 1; fi\n" +
		`printf '%s\n' '{"message_type":"exit_error","code":11,"message":"Fatal: unable to create lock in backend: repository is already locked\n"}' >&2` +
		"\nexit 11\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	w := &Wrapper{resticBin: script, rcloneBin: "/fake/rclone", logger: zap.NewNop()}
	dest := Destination{Type: DestLocal, RepoURL: dir, Password: "pw"}

	err := w.run(context.Background(), dest, []string{"forget"})
	want := "restic: unable to create lock in backend: repository is already locked (exit status 11)"
	if err == nil || err.Error() != want {
		t.Fatalf("run() = %v, want %q", err, want)
	}
	if !isLockConflict(err) {
		t.Error("isLockConflict(err) = false, want true")
	}
}

// TestEscapeGlob: restic reads --exclude values as glob patterns, so a
// literal path must have its pattern characters escaped (except on Windows,
// where the backslash is the path separator and cannot escape).
func TestEscapeGlob(t *testing.T) {
	tests := []struct {
		in, goos, want string
	}{
		{"/srv/app", "linux", "/srv/app"},
		{"/srv/app[1]", "linux", `/srv/app\[1]`},
		{"/srv/b*x?", "linux", `/srv/b\*x\?`},
		{`/srv/c\d`, "linux", `/srv/c\\d`},
		{"/srv/app[1]", "darwin", `/srv/app\[1]`},
		{`C:\srv\app[1]`, "windows", `C:\srv\app[1]`},
	}
	for _, tt := range tests {
		if got := escapeGlob(tt.in, tt.goos); got != tt.want {
			t.Errorf("escapeGlob(%q, %q) = %q, want %q", tt.in, tt.goos, got, tt.want)
		}
	}
}

// TestBuildRestoreArgs_EscapesPaths: include and exclude paths are literal
// paths, passed to restic escaped.
func TestBuildRestoreArgs_EscapesPaths(t *testing.T) {
	args := buildRestoreArgs("latest", "/", []string{"/srv/app[1]"}, []string{"/srv/b*x"}, "linux")
	for _, want := range [][2]string{{"--include", `/srv/app\[1]`}, {"--exclude", `/srv/b\*x`}} {
		i := slices.Index(args, want[0])
		if i == -1 || args[i+1] != want[1] {
			t.Errorf("args = %v, want %s %q", args, want[0], want[1])
		}
	}
}

func TestErrorMessage_FallbackKeepsTail(t *testing.T) {
	stderr := strings.Repeat("noise line\n", 1000) + "Fatal: the real cause"
	got := errorMessage(stderr)
	if len(got) > maxErrorFallback+len("…") {
		t.Errorf("errorMessage() is %d bytes, want at most %d", len(got), maxErrorFallback+len("…"))
	}
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "Fatal: the real cause") {
		t.Errorf("errorMessage() = %q…, want the tail of stderr, marked as cut", got[:40])
	}
}

// selinuxRemoveStderr is the stderr of restic 0.19.1 reported in issue #424:
// restoring a file whose snapshot node has no security.selinux xattr onto an
// SELinux host, where the kernel labels every new file and refuses to remove
// the label, even for root.
const selinuxRemoveStderr = `{"message_type":"error","error":{"message":"xattr.LRemove /opt/arkeep-agent/restore/mysql-dump security.selinux: permission denied"},"during":"restore","item":"/mysql-dump"}
{"message_type":"exit_error","code":1,"message":"Fatal: There were 1 errors"}`

// TestBuildRestoreArgs_SELinuxLabel verifies issue #424: a restore to a folder
// on Linux leaves the security.selinux xattr to the destination's policy
// (--exclude-xattr), while an in-place restore ("/") keeps restoring the
// labels saved in the snapshot, and other systems are left unchanged.
func TestBuildRestoreArgs_SELinuxLabel(t *testing.T) {
	tests := []struct {
		name      string
		targetDir string
		goos      string
		want      bool
	}{
		{"linux folder restore", "/opt/arkeep-agent/restore", "linux", true},
		{"linux in-place restore", "/", "linux", false},
		{"darwin folder restore", "/Users/me/restore", "darwin", false},
		{"windows folder restore", `C:\restore`, "windows", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := buildRestoreArgs("abc123", tt.targetDir, nil, nil, tt.goos)
			end := slices.Index(args, "--")
			i := slices.Index(args, "--exclude-xattr")
			got := i != -1 && i+1 < len(args) && args[i+1] == "security.selinux" && i < end
			if got != tt.want {
				t.Errorf("buildRestoreArgs(target %q, %s) = %v, want --exclude-xattr security.selinux: %v", tt.targetDir, tt.goos, args, tt.want)
			}
		})
	}
}

// TestIsOnlyToleratedRestoreErrors covers the restic restore errors that do
// not mean data loss: the removal of an SELinux label (issue #424) anywhere,
// and lchown failures under hostRoot (Docker deployments on NTFS).
func TestIsOnlyToleratedRestoreErrors(t *testing.T) {
	const lchownHostRoot = `{"message_type":"error","error":{"message":"lchown /hostfs/c: permission denied"},"during":"restore","item":"/hostfs/c"}`
	const otherError = `{"message_type":"error","error":{"message":"open /data/file: no space left on device"},"during":"restore","item":"/data/file"}`
	const exitError = `{"message_type":"exit_error","code":1,"message":"Fatal: There were 2 errors"}`
	tests := []struct {
		name     string
		stderr   string
		hostRoot string
		want     bool
	}{
		{"selinux label removal (issue #424)", selinuxRemoveStderr, "", true},
		{"selinux label removal with hostRoot", selinuxRemoveStderr, "/hostfs", true},
		{"selinux label removal plain text", "ignoring error for /mysql-dump: xattr.LRemove /restore/mysql-dump security.selinux: permission denied\nFatal: There were 1 errors", "", true},
		{"selinux label and hostRoot lchown", lchownHostRoot + "\n" + selinuxRemoveStderr, "/hostfs", true},
		{"lchown under hostRoot", lchownHostRoot + "\n" + exitError, "/hostfs", true},
		{"lchown without hostRoot", lchownHostRoot + "\n" + exitError, "", false},
		{"selinux label and another error", otherError + "\n" + selinuxRemoveStderr, "", false},
		{"removal of another xattr", `{"message_type":"error","error":{"message":"xattr.LRemove /restore/f user.comment: permission denied"},"during":"restore","item":"/f"}`, "", false},
		{"setting the selinux label", `{"message_type":"error","error":{"message":"xattr.LSet /restore/f security.selinux: invalid argument"},"during":"restore","item":"/f"}`, "", false},
		{"fatal error", "Fatal: wrong password or no key found", "", false},
		{"no errors", exitError, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isOnlyToleratedRestoreErrors(tt.stderr, tt.hostRoot); got != tt.want {
				t.Errorf("isOnlyToleratedRestoreErrors(hostRoot %q) = %v, want %v", tt.hostRoot, got, tt.want)
			}
		})
	}
}

// TestRestore_InPlaceToleratesSELinuxLabelRemoval runs Restore against a fake
// restic that fails exactly as in issue #424: the in-place restore succeeds,
// since the data was written and only the label removal was refused.
func TestRestore_InPlaceToleratesSELinuxLabelRemoval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake restic is a shell script")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-restic.sh")
	body := fmt.Sprintf("#!/bin/sh\necho '{\"message_type\":\"summary\",\"total_files\":1,\"files_restored\":1}'\ncat >&2 <<'EOF'\n%s\nEOF\nexit 1\n", selinuxRemoveStderr)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	w := &Wrapper{resticBin: script, rcloneBin: "/fake/rclone", logger: zap.NewNop()}
	dest := Destination{Type: DestLocal, RepoURL: dir, Password: "pw"}

	if err := w.Restore(context.Background(), dest, "abc123", "/", nil, nil, ""); err != nil {
		t.Fatalf("Restore() = %v, want nil: only the SELinux label removal failed", err)
	}
}
