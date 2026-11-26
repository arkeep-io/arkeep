package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkeep-io/arkeep/shared/proto"
)

// These tests cover the check that refuses a local destination an agent in a
// container would write to its own writable layer or to a tmpfs (issue #502).
// The mounts come from a mountinfo file written by the test.

// writeMountinfo writes a mountinfo file with an overlay root and, for each
// point, a bind mount there; it returns its path.
func writeMountinfo(t *testing.T, points ...string) string {
	t.Helper()
	content := "1 0 0:1 / / rw,relatime - overlay overlay rw\n"
	for i, p := range points {
		content += fmt.Sprintf("%d 1 8:1 /srv %s rw - ext4 /dev/sda1 rw\n", i+2, p)
	}
	path := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// backupTo runs a backup of e2eSource to the local destination repoURL, as
// the server sends it, and returns the reporter and the log sink.
func (env *e2eEnv) backupTo(t *testing.T, repoURL string) (*fakeReporter, *recordingSink) {
	t.Helper()
	src := env.hostPath(e2eSource)
	if err := os.MkdirAll(src, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	srcJSON, err := json.Marshal([]string{env.path(e2eSource)})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(backupPayload{
		Sources:      string(srcJSON),
		RepoPassword: "test-password",
		Destinations: []destinationPayload{{DestinationID: "dest-1", Type: "local", RepoURL: repoURL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordingSink{}
	reporter := &fakeReporter{}
	env.e.executeBackup(context.Background(), JobAssignment{JobID: "backup-1", Type: proto.JobType_JOB_TYPE_BACKUP, Payload: raw}, sink, reporter)
	return reporter, sink
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestBackup_EphemeralLocalDestination_NewRepositoryFails(t *testing.T) {
	env := newE2EEnv(t, false)
	env.e.CheckEphemeralLocalDestinations()
	env.e.mountinfoPath = writeMountinfo(t) // nothing but the overlay root

	repo := filepath.Join(t.TempDir(), "new-repo")
	reporter, sink := env.backupTo(t, repo)

	res := reporter.destResults["dest-1"]
	if res.status != "failed" {
		t.Fatalf("destination status = %q, want failed; logs:\n%v", res.status, sink.logs)
	}
	for _, want := range []string{repo, "container's own filesystem", "lost when the container is recreated", "ARKEEP_ALLOW_EPHEMERAL_LOCAL_DESTINATION"} {
		if !strings.Contains(res.errMsg, want) {
			t.Errorf("error %q does not mention %q", res.errMsg, want)
		}
	}
	if strings.Contains(res.errMsg, "can still be restored") {
		t.Errorf("error %q talks about snapshots of a repository that does not exist", res.errMsg)
	}
	if _, err := os.Stat(repo); !os.IsNotExist(err) {
		t.Errorf("the check created %s on the container's layer (stat err %v)", repo, err)
	}
}

func TestBackup_EphemeralLocalDestination_ExistingRepositoryFails(t *testing.T) {
	env := newE2EEnv(t, false)
	env.e.CheckEphemeralLocalDestinations()
	env.e.mountinfoPath = writeMountinfo(t)

	reporter, _ := env.backupTo(t, env.repoURL)

	res := reporter.destResults["dest-1"]
	if res.status != "failed" {
		t.Fatalf("destination status = %q, want failed", res.status)
	}
	if !strings.Contains(res.errMsg, "can still be restored") {
		t.Errorf("error %q does not say the snapshots already there can be restored", res.errMsg)
	}
}

func TestBackup_EphemeralLocalDestination_TmpfsFails(t *testing.T) {
	env := newE2EEnv(t, false)
	env.e.CheckEphemeralLocalDestinations()
	dir := realPath(t, t.TempDir())
	mountinfo := filepath.Join(t.TempDir(), "mountinfo")
	content := "1 0 0:1 / / rw - overlay overlay rw\n2 1 0:2 / " + dir + " rw - tmpfs tmpfs rw\n"
	if err := os.WriteFile(mountinfo, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	env.e.mountinfoPath = mountinfo

	reporter, _ := env.backupTo(t, filepath.Join(dir, "repo"))

	res := reporter.destResults["dest-1"]
	if res.status != "failed" || !strings.Contains(res.errMsg, "tmpfs") {
		t.Fatalf("destination = %+v, want failed naming the tmpfs", res)
	}
}

func TestBackup_EphemeralLocalDestination_TranslatedPathNamesHostRoot(t *testing.T) {
	env := newE2EEnv(t, true)
	env.e.CheckEphemeralLocalDestinations()
	env.e.mountinfoPath = writeMountinfo(t)

	reporter, _ := env.backupTo(t, "/backups/new-repo")

	res := reporter.destResults["dest-1"]
	if res.status != "failed" {
		t.Fatalf("destination status = %q, want failed", res.status)
	}
	for _, want := range []string{`translated from "/backups/new-repo"`, "ARKEEP_DOCKER_HOST_ROOT"} {
		if !strings.Contains(res.errMsg, want) {
			t.Errorf("error %q does not mention %q", res.errMsg, want)
		}
	}
}

func TestBackup_EphemeralLocalDestination_BindMountSucceeds(t *testing.T) {
	env := newE2EEnv(t, false)
	env.e.CheckEphemeralLocalDestinations()
	parent := realPath(t, t.TempDir())
	env.e.mountinfoPath = writeMountinfo(t, parent)

	reporter, sink := env.backupTo(t, filepath.Join(parent, "repo"))

	if got := lastStatus(reporter); got != "succeeded" {
		t.Fatalf("backup status = %q (%s), want succeeded; logs:\n%v", got, reporter.lastMsg, sink.logs)
	}
}

func TestBackup_EphemeralLocalDestination_UnreadableMountinfoBacksUp(t *testing.T) {
	env := newE2EEnv(t, false)
	env.e.CheckEphemeralLocalDestinations()
	env.e.mountinfoPath = filepath.Join(t.TempDir(), "missing")

	reporter, sink := env.backupTo(t, env.repoURL)

	if got := lastStatus(reporter); got != "succeeded" {
		t.Fatalf("backup status = %q (%s), want succeeded; logs:\n%v", got, reporter.lastMsg, sink.logs)
	}
	if !sink.contains("could not tell whether") {
		t.Errorf("no warning about the skipped check; logs:\n%v", sink.logs)
	}
}

func TestBackup_EphemeralLocalDestination_CheckOffByDefault(t *testing.T) {
	// A native agent, or one with ARKEEP_ALLOW_EPHEMERAL_LOCAL_DESTINATION:
	// main.go never enables the check, so even an overlay root is accepted.
	env := newE2EEnv(t, false)
	env.e.mountinfoPath = writeMountinfo(t)

	reporter, sink := env.backupTo(t, env.repoURL)

	if got := lastStatus(reporter); got != "succeeded" {
		t.Fatalf("backup status = %q (%s), want succeeded; logs:\n%v", got, reporter.lastMsg, sink.logs)
	}
}
