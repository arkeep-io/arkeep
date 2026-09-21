package restic

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	args := buildRestoreArgs("--password-command=touch /tmp/pwned", "/restore/target", []string{"/inc"}, []string{"/exc"})

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
		{
			// A command source's own retention pool (see buildStdinBackupArgs):
			// it must be the ONLY tag, never combined with the bare
			// "policy:<id>" tag also used by the regular pool, or forgetting
			// one pool would sweep the other's snapshots too.
			name:   "command source retention tag is passed through verbatim",
			policy: RetentionPolicy{Last: 7},
			tags:   []string{"policy:abc:command:pgdump"},
			want: []string{
				"forget", "--prune", "--json",
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
