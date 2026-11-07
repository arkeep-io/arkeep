package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkeep-io/arkeep/agent/internal/docker"
)

// In a Docker deployment the agent sees Docker's named volumes twice: at the
// Docker volumes root itself (bind-mounted at the same path) and again under
// the host root (/hostfs/var/lib/docker/volumes), because the host's "/" is
// mounted too. A backup of /var/lib or / as a path source stores the volumes
// under the host-root alias, so an in-place restore must protect them there
// as well.

// fakeDocker is a dockerAPI that reports a fixed set of running containers
// and volume mountpoints.
type fakeDocker struct {
	running     []docker.RunningContainerVolumes
	mountpoints map[string]string // volume name -> host mountpoint
	err         error             // returned by ListRunningContainerVolumes when set
}

func (f *fakeDocker) InspectVolume(_ context.Context, name string) (*docker.VolumeInfo, error) {
	mp, ok := f.mountpoints[name]
	if !ok {
		return nil, errors.New("fakeDocker: no such volume")
	}
	return &docker.VolumeInfo{Name: name, Mountpoint: mp, Driver: "local"}, nil
}

func (f *fakeDocker) ListRunningContainerVolumes(context.Context) ([]docker.RunningContainerVolumes, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.running, nil
}

// volumeFixture is a Docker deployment whose volumes root holds volume
// app_data, reached through the host root.
type volumeFixture struct {
	env     *e2eEnv
	volRoot string // e.dockerVolRoot, host form (also where the agent sees it directly)
	alias   string // volRoot under the host root
	volData string // <volRoot>/app_data/_data, as Docker reports it
	dbTxt   string // db.txt under the alias, where the snapshot stores it
	normal  string // a regular host file (on disk)
}

const (
	volContent    = "db v1\n"
	normalContent = "data v1\n"
	damaged       = "damaged\n"
)

func newVolumeFixture(t *testing.T) *volumeFixture {
	t.Helper()
	env := newE2EEnv(t, true)
	f := &volumeFixture{env: env, volRoot: t.TempDir() + "/vols"}
	env.e.dockerVolRoot = f.volRoot
	f.alias = env.hostPath(f.volRoot)
	f.volData = f.volRoot + "/app_data/_data"
	f.dbTxt = f.alias + "/app_data/_data/db.txt"
	f.normal = env.hostPath(e2eSource + "/a.txt")
	if err := os.MkdirAll(f.volRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, f.dbTxt, volContent)
	writeFile(t, f.normal, normalContent)
	return f
}

// runningApp makes Docker report container "app" using volume app_data.
func (f *volumeFixture) runningApp() {
	f.env.e.docker = &fakeDocker{
		running:     []docker.RunningContainerVolumes{{ContainerName: "app", VolumePaths: []string{f.volData}}},
		mountpoints: map[string]string{"app_data": f.volData},
	}
}

// damage overwrites both files so a restore has something to undo.
func (f *volumeFixture) damage(t *testing.T) {
	t.Helper()
	writeFile(t, f.dbTxt, damaged)
	writeFile(t, f.normal, damaged)
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertContent(t *testing.T, p, want, what string) {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", what, err)
	}
	if string(data) != want {
		t.Errorf("%s after restore = %q, want %q", what, data, want)
	}
}

func TestInPlace_RunningVolumeViaHostRootIsSkipped(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	// As a user backing up /var/lib would: the volumes are stored under the
	// host-root alias.
	id := f.env.backup(t, f.volRoot, e2eSource)
	f.runningApp()
	f.damage(t)

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: "/"}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, f.normal, normalContent, "normal file")
	assertContent(t, f.dbTxt, damaged, "db.txt of running container's volume")
	if !sink.contains(`container "app" is running`) {
		t.Errorf("no running-container warning in logs:\n%v", sink.logs)
	}
}

func TestInPlace_IncludeInsideAliasVolumeIsSkipped(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	id := f.env.backup(t, f.volRoot, e2eSource)
	f.runningApp()
	f.damage(t)

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{
		ResticSnapshotID: id, TargetPath: "/", IncludePaths: []string{f.dbTxt, f.normal},
	}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, f.normal, normalContent, "normal file")
	assertContent(t, f.dbTxt, damaged, "db.txt of running container's volume")
	if want := fmt.Sprintf("skipping %q for in-place restore", f.dbTxt); !sink.contains(want) {
		t.Errorf("no %q warning in logs:\n%v", want, sink.logs)
	}
}

func TestInPlace_IncludeAncestorOfAliasVolumeFails(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	id := f.env.backup(t, f.volRoot, e2eSource)
	f.runningApp()
	f.damage(t)

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{
		ResticSnapshotID: id, TargetPath: "/", IncludePaths: []string{filepath.Dir(f.alias)},
	}, sink)
	mustEndAs(t, r, sink, "failed")
	if !strings.Contains(r.lastMsg, "cannot restore") {
		t.Errorf("failure message = %q, want it to contain %q", r.lastMsg, "cannot restore")
	}
	assertContent(t, f.dbTxt, damaged, "db.txt")
}

func TestInPlace_ReadOnlyVolumesRootExcluded(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	f := newVolumeFixture(t)
	id := f.env.backup(t, f.volRoot, e2eSource)
	f.damage(t)
	for _, root := range []string{f.volRoot, f.alias} {
		if err := os.Chmod(root, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(root, 0o755) })
	}

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: "/"}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, f.normal, normalContent, "normal file")
	assertContent(t, f.dbTxt, damaged, "db.txt under a read-only volumes root")
	for _, want := range []string{
		"restore: " + f.volRoot + " is not writable",
		"restore: " + f.alias + " (the host's Docker volumes directory under the host-root mount " + f.env.hostRoot + ") is not writable",
		"requires the host-root mount to be writable and the containers using those volumes stopped",
	} {
		if !sink.contains(want) {
			t.Errorf("no %q warning in logs:\n%v", want, sink.logs)
		}
	}
}

// TestInPlace_ReadOnlyDirectRootRunningVolumeViaAlias is the compose default
// after the user switched /:/hostfs to :rw: the direct volumes mount is still
// :ro, the alias under the host root is writable, and a container is running.
// The read-only direct root must not stop the running-container check.
func TestInPlace_ReadOnlyDirectRootRunningVolumeViaAlias(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so the direct root cannot be made read-only")
	}
	f := newVolumeFixture(t)
	stopped := f.alias + "/other_data/_data/db.txt"
	writeFile(t, stopped, volContent)
	id := f.env.backup(t, f.volRoot, e2eSource)
	f.runningApp()
	f.damage(t)
	writeFile(t, stopped, damaged)
	if err := os.Chmod(f.volRoot, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.volRoot, 0o755) })

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: "/"}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, f.normal, normalContent, "normal file")
	assertContent(t, f.dbTxt, damaged, "db.txt of running container's volume")
	assertContent(t, stopped, volContent, "db.txt of stopped volume")
	for _, want := range []string{
		"restore: " + f.volRoot + " is not writable",
		`container "app" is running`,
	} {
		if !sink.contains(want) {
			t.Errorf("no %q warning in logs:\n%v", want, sink.logs)
		}
	}
}

// TestInPlace_DockerVolumeSourceStillSkipped is the existing case: the volume
// was backed up at its own mountpoint (docker-volume:// source), not through
// the host root.
func TestInPlace_DockerVolumeSourceStillSkipped(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	direct := f.volData + "/db.txt"
	writeFile(t, direct, volContent)
	f.runningApp()
	id := f.env.backup(t, "docker-volume://app_data")
	writeFile(t, direct, damaged)

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: "/"}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, direct, damaged, "db.txt of running container's volume")
	if !sink.contains(`container "app" is running`) {
		t.Errorf("no running-container warning in logs:\n%v", sink.logs)
	}
}

// TestInPlace_NoDockerClientWarnsWritableRoots: without a Docker client (the
// socket is not mounted) the agent cannot tell which containers are running,
// so writable volumes roots are restored in place. That is unchanged, but the
// user must be warned to stop the containers first.
func TestInPlace_NoDockerClientWarnsWritableRoots(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	id := f.env.backup(t, f.volRoot, e2eSource)
	f.damage(t) // e.docker stays nil

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: "/"}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, f.normal, normalContent, "normal file")
	assertContent(t, f.dbTxt, volContent, "db.txt (restored in place: nothing to check it against)")
	want := "cannot check which containers are running (Docker socket not available): " +
		"Docker volumes under " + f.volRoot + ", " + f.alias + " will be restored in place"
	if !sink.contains(want) {
		t.Errorf("no %q warning in logs:\n%v", want, sink.logs)
	}
}

// TestInPlace_ListRunningContainersFails: when Docker cannot list the running
// containers, the restore proceeds without per-container exclusions (current
// behaviour, warned about): a writable alias is restored in place even though
// a container may be using the volume.
func TestInPlace_ListRunningContainersFails(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	id := f.env.backup(t, f.volRoot, e2eSource)
	f.env.e.docker = &fakeDocker{err: errors.New("docker daemon unreachable")}
	f.damage(t)

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: "/"}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, f.normal, normalContent, "normal file")
	assertContent(t, f.dbTxt, volContent, "db.txt (no per-container exclusions)")
	want := "could not list running containers for in-place restore: docker daemon unreachable — " +
		"proceeding without per-container exclusions"
	if !sink.contains(want) {
		t.Errorf("no %q warning in logs:\n%v", want, sink.logs)
	}
}
