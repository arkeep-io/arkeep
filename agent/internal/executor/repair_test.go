package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/agent/internal/restic"
	"github.com/arkeep-io/arkeep/shared/proto"
	"github.com/arkeep-io/arkeep/shared/repair"
)

const repairTestPassword = "s3cr3t-repo-password"

// fakeRestic writes a restic stand-in that records its arguments and whether
// RESTIC_PASSWORD reached its environment, prints two lines and exits with
// code.
func fakeRestic(t *testing.T, code int) (bin, argsFile, envFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	envFile = filepath.Join(dir, "env")
	bin = filepath.Join(dir, "restic")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q
[ "$RESTIC_PASSWORD" = %q ] && echo password-in-env > %q
echo "repository 1234abcd opened"
echo ""
echo "warning from restic" >&2
exit %d
`, argsFile, repairTestPassword, envFile, code)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile, envFile
}

func repairJob(t *testing.T, p repair.Params) JobAssignment {
	t.Helper()
	raw, err := json.Marshal(repairPayload{
		Destination:  destinationPayload{DestinationID: "dest-1", Type: "s3", RepoURL: "s3:s3.example.com/b/"},
		RepoPassword: repairTestPassword,
		Repair:       p,
	})
	if err != nil {
		t.Fatal(err)
	}
	return JobAssignment{JobID: "job-1", Type: proto.JobType_JOB_TYPE_REPAIR, Payload: raw}
}

func TestExecuteRepair_StreamsOutputAndKeepsThePasswordInTheEnvironment(t *testing.T) {
	bin, argsFile, envFile := fakeRestic(t, 0)
	e := New(restic.NewWrapperWithBinaries(bin, "/fake/rclone", zap.NewNop()), nil, nil, zap.NewNop(), "", "")
	sink := &recordingSink{}
	reporter := &fakeReporter{}

	e.execute(context.Background(), repairJob(t, repair.Params{Action: repair.Snapshots, Hosts: []string{"helios"}}), sink, reporter)

	if !slices.Equal(reporter.statuses, []string{"running", "succeeded"}) {
		t.Fatalf("statuses = %v, want running then succeeded (last message %q)", reporter.statuses, reporter.lastMsg)
	}
	if got := reporter.destResults["dest-1"].status; got != "succeeded" {
		t.Errorf("destination status = %q, want succeeded", got)
	}
	for _, want := range []string{"running restic repair snapshots --dry-run --host helios --", "repository 1234abcd opened", "warning from restic", "exited with code 0"} {
		if !sink.contains(want) {
			t.Errorf("job log lacks %q: %q", want, sink.logs)
		}
	}
	for _, l := range sink.logs {
		if strings.TrimSpace(l) == "" {
			t.Error("a blank line of restic's output reached the job log")
		}
	}
	if sink.contains(repairTestPassword) {
		t.Error("the repository password reached the job log")
	}

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), repairTestPassword) {
		t.Error("the repository password reached restic's arguments")
	}
	if _, err := os.Stat(envFile); err != nil {
		t.Error("restic did not receive the repository password in its environment")
	}
}

func TestExecuteRepair_ReportsResticExitCode(t *testing.T) {
	bin, _, _ := fakeRestic(t, 11)
	e := New(restic.NewWrapperWithBinaries(bin, "/fake/rclone", zap.NewNop()), nil, nil, zap.NewNop(), "", "")
	reporter := &fakeReporter{}

	e.execute(context.Background(), repairJob(t, repair.Params{Action: repair.Index}), fakeSink{}, reporter)

	if final := reporter.statuses[len(reporter.statuses)-1]; final != "failed" {
		t.Fatalf("final status = %q, want failed", final)
	}
	if !strings.Contains(reporter.lastMsg, "exited with code 11") || !strings.Contains(reporter.lastMsg, "locked by another operation") {
		t.Errorf("failure message = %q, want the exit code and the lock explanation", reporter.lastMsg)
	}
	if got := reporter.destResults["dest-1"]; got.status != "failed" || got.errMsg != reporter.lastMsg {
		t.Errorf("destination result = %+v, want failed with the same message", got)
	}
}

// TestExecuteRepair_InvalidRequestNeverRunsRestic: the agent validates a
// payload again, so one that bypassed the server fails before restic starts.
func TestExecuteRepair_InvalidRequestNeverRunsRestic(t *testing.T) {
	bin, argsFile, _ := fakeRestic(t, 0)
	e := New(restic.NewWrapperWithBinaries(bin, "/fake/rclone", zap.NewNop()), nil, nil, zap.NewNop(), "", "")
	reporter := &fakeReporter{}

	e.execute(context.Background(), repairJob(t, repair.Params{Action: repair.FindPacks, PackIDs: []string{"--password-command=sh"}}), fakeSink{}, reporter)

	if !slices.Equal(reporter.statuses, []string{"failed"}) || !strings.Contains(reporter.lastMsg, "invalid repair request") {
		t.Errorf("statuses = %v (%q), want only failed with the validation error", reporter.statuses, reporter.lastMsg)
	}
	if _, err := os.Stat(argsFile); err == nil {
		t.Error("restic ran for an invalid request")
	}
}

func TestRepairFailure(t *testing.T) {
	for _, tt := range []struct {
		params repair.Params
		code   int
		want   string
	}{
		{repair.Params{Action: repair.Index}, 10, "does not exist"},
		{repair.Params{Action: repair.Index}, 12, "wrong repository password"},
		{repair.Params{Action: repair.Check}, 1, "the repository has errors"},
		{repair.Params{Action: repair.Index}, 1, "restic exited with code 1"},
		{repair.Params{Action: repair.Index}, -1, "did not exit normally"},
	} {
		if got := repairFailure(tt.params, tt.code); !strings.Contains(got, tt.want) {
			t.Errorf("repairFailure(%s, %d) = %q, want it to contain %q", tt.params.Action, tt.code, got, tt.want)
		}
	}
}
