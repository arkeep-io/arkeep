package executor

import (
	"context"
	"errors"
	"fmt"
	"net"
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
	volumesRoot string            // returned by VolumesRoot; empty makes it fail
}

func (f *fakeDocker) VolumesRoot(context.Context) (string, error) {
	if f.volumesRoot == "" {
		return "", errors.New("fakeDocker: no volumes root")
	}
	return f.volumesRoot, nil
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
		volumesRoot: f.volRoot,
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
// so it fails closed: the writable volumes roots are left out of the in-place
// restore, with a warning, and everything else is restored.
func TestInPlace_NoDockerClientWarnsWritableRoots(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	id := f.env.backup(t, f.volRoot, e2eSource)
	f.damage(t) // e.docker stays nil

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: "/"}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, f.normal, normalContent, "normal file")
	assertContent(t, f.dbTxt, damaged, "db.txt (running containers could not be checked)")
	for _, want := range []string{
		"cannot check which containers are running (Docker was not reachable when the agent started), so Docker volumes under " +
			f.volRoot + ", " + f.alias + " were skipped by the in-place restore",
		"mount the Docker socket",
		"restore to a separate folder and copy the files back",
	} {
		if !sink.contains(want) {
			t.Errorf("no %q warning in logs:\n%v", want, sink.logs)
		}
	}
}

// TestInPlace_ListRunningContainersFails: when Docker cannot list the running
// containers, the writable volumes roots are left out of the in-place restore,
// as without a Docker client.
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
	assertContent(t, f.dbTxt, damaged, "db.txt (running containers could not be checked)")
	want := "cannot check which containers are running (listing them failed: docker daemon unreachable), " +
		"so Docker volumes under " + f.volRoot + ", " + f.alias + " were skipped by the in-place restore"
	if !sink.contains(want) {
		t.Errorf("no %q warning in logs:\n%v", want, sink.logs)
	}
}

// TestInPlace_NoDockerClientIncludeInsideVolumesRootFails: a targeted
// restore of only volume files, with the volumes roots left out, has nothing
// left to restore.
func TestInPlace_NoDockerClientIncludeInsideVolumesRootFails(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	id := f.env.backup(t, f.volRoot, e2eSource)
	f.damage(t)

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{
		ResticSnapshotID: id, TargetPath: "/", IncludePaths: []string{f.dbTxt},
	}, sink)
	mustEndAs(t, r, sink, "failed")
	if !strings.Contains(r.lastMsg, "nothing to restore in place") {
		t.Errorf("failure message = %q, want it to contain %q", r.lastMsg, "nothing to restore in place")
	}
	assertContent(t, f.dbTxt, damaged, "db.txt")
}

// TestInPlace_CustomDataRootFromDocker: Docker reports its volumes root (a
// custom data-root here), and the agent checks that root, not its default.
func TestInPlace_CustomDataRootFromDocker(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	f := newVolumeFixture(t)
	id := f.env.backup(t, f.volRoot, e2eSource)
	f.env.e.dockerVolRoot = filepath.Join(t.TempDir(), "default-volumes") // not where Docker keeps them
	f.env.e.docker = &fakeDocker{volumesRoot: f.volRoot}
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
	assertContent(t, f.dbTxt, damaged, "db.txt under the reported, read-only volumes root")
	if want := "restore: " + f.alias + " (the host's Docker volumes directory"; !sink.contains(want) {
		t.Errorf("no %q warning in logs:\n%v", want, sink.logs)
	}
}

// TestInPlace_RunningVolumeExcludedWithoutVisibleRoot: Docker keeps its
// volumes where the agent cannot see the root (e.g. inside the Docker Desktop
// VM), yet a running container's volume is still excluded.
func TestInPlace_RunningVolumeExcludedWithoutVisibleRoot(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	id := f.env.backup(t, f.volRoot, e2eSource)
	f.runningApp()
	f.env.e.dockerVolRoot = filepath.Join(t.TempDir(), "default-volumes")
	f.env.e.docker.(*fakeDocker).volumesRoot = filepath.Join(t.TempDir(), "vm", "volumes")
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

// bindSource is the host path a running container bind-mounts read-write.
const bindSource = "/srv/app"

// runningBind makes Docker report container "web" with a writable bind mount
// of bindSource, writes a file there and returns its path on disk.
func (f *volumeFixture) runningBind(t *testing.T) string {
	t.Helper()
	f.env.e.docker = &fakeDocker{
		running:     []docker.RunningContainerVolumes{{ContainerName: "web", BindPaths: []string{bindSource}}},
		volumesRoot: f.volRoot,
	}
	p := f.env.hostPath(bindSource + "/state.txt")
	writeFile(t, p, volContent)
	return p
}

func TestInPlace_RunningBindMountIsSkipped(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	state := f.runningBind(t)
	id := f.env.backup(t, bindSource, e2eSource)
	f.damage(t)
	writeFile(t, state, damaged)

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: "/"}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, f.normal, normalContent, "normal file")
	assertContent(t, state, damaged, "file in running container's bind mount")
	want := `skipping bind mount "` + bindSource + `" for in-place restore: container "web" is running. Stop it first to restore this path.`
	if !sink.contains(want) {
		t.Errorf("no %q warning in logs:\n%v", want, sink.logs)
	}
}

func TestInPlace_IncludeInsideBindMountIsSkipped(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	state := f.runningBind(t)
	id := f.env.backup(t, bindSource, e2eSource)
	f.damage(t)
	writeFile(t, state, damaged)

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{
		ResticSnapshotID: id, TargetPath: "/", IncludePaths: []string{state, f.normal},
	}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, f.normal, normalContent, "normal file")
	assertContent(t, state, damaged, "file in running container's bind mount")
	for _, want := range []string{
		fmt.Sprintf("skipping %q for in-place restore", state),
		`skipping bind mount "` + bindSource + `"`,
	} {
		if !sink.contains(want) {
			t.Errorf("no %q warning in logs:\n%v", want, sink.logs)
		}
	}
}

// TestInPlace_IncludeAncestorOfBindMountFails: restic 0.19.1 rejects
// --include together with --exclude ("exclude and include patterns are
// mutually exclusive"), so an include containing a running bind mount cannot
// be restored in place minus the bind mount.
func TestInPlace_IncludeAncestorOfBindMountFails(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	state := f.runningBind(t)
	id := f.env.backup(t, bindSource, e2eSource)
	writeFile(t, state, damaged)

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{
		ResticSnapshotID: id, TargetPath: "/", IncludePaths: []string{f.env.hostPath("/srv")},
	}, sink)
	mustEndAs(t, r, sink, "failed")
	if !strings.Contains(r.lastMsg, "cannot restore") {
		t.Errorf("failure message = %q, want it to contain %q", r.lastMsg, "cannot restore")
	}
	assertContent(t, state, damaged, "file in running container's bind mount")
}

// TestInPlace_UnrelatedIncludeHasNoDockerWarnings: a targeted restore away
// from any excluded path is not cluttered with warnings about them.
func TestInPlace_UnrelatedIncludeHasNoDockerWarnings(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	id := f.env.backup(t, f.volRoot, e2eSource)
	f.damage(t) // e.docker stays nil: the volumes roots are excluded

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{
		ResticSnapshotID: id, TargetPath: "/", IncludePaths: []string{f.normal},
	}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, f.normal, normalContent, "normal file")
	if sink.contains("Docker volumes under") {
		t.Errorf("warning about volumes roots for an unrelated include:\n%v", sink.logs)
	}
}

// TestInPlace_AgentAndVolumesRootBindsIgnored: the agent's own writable bind
// mounts (the documented :rw volumes mount, a partial host mount) and another
// container's bind of the whole volumes root (e.g. a Portainer agent) must
// not block the restore: running containers' volumes are excluded one by
// one anyway.
func TestInPlace_AgentAndVolumesRootBindsIgnored(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	stopped := f.alias + "/other_data/_data/db.txt"
	writeFile(t, stopped, volContent)
	id := f.env.backup(t, f.volRoot, e2eSource)
	const self = "0123456789ab"
	f.env.e.selfContainerID = self
	f.env.e.docker = &fakeDocker{
		volumesRoot: f.volRoot,
		running: []docker.RunningContainerVolumes{
			{ContainerID: self + "cdef0123456789", ContainerName: "arkeep-agent",
				BindPaths: []string{f.volRoot, "/home/u"}, VolumePaths: []string{"/var/lib/docker/volumes/agent_state/_data"},
				MountDestinations: []string{"/var/lib/arkeep-agent", f.env.hostRoot + "/home/u"}},
			{ContainerID: "fedcba9876543210", ContainerName: "portainer_agent", BindPaths: []string{f.volRoot}},
			{ContainerID: "1111111111111111", ContainerName: "app", VolumePaths: []string{f.volData}},
		},
	}
	f.damage(t)
	writeFile(t, stopped, damaged)

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: "/"}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, f.normal, normalContent, "normal file under the agent's own bind mount")
	assertContent(t, stopped, volContent, "db.txt of stopped container's volume")
	assertContent(t, f.dbTxt, damaged, "db.txt of running container's volume")
	if sink.contains("skipping bind mount") {
		t.Errorf("bind mount warning for the agent's own or a volumes-root bind:\n%v", sink.logs)
	}
	if !sink.contains(`container "app" is running`) {
		t.Errorf("no running-container warning in logs:\n%v", sink.logs)
	}
}

// TestInPlace_SocketBindIgnored: a running container bind-mounting the Docker
// socket read-write (Watchtower, Portainer, Traefik) is not a reason to warn.
func TestInPlace_SocketBindIgnored(t *testing.T) {
	t.Parallel()
	// A short directory: unix socket paths are limited to ~108 bytes.
	dir, err := os.MkdirTemp("", "ak")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	f := newVolumeFixture(t)
	id := f.env.backup(t, e2eSource)
	f.env.e.docker = &fakeDocker{
		volumesRoot: f.volRoot,
		running:     []docker.RunningContainerVolumes{{ContainerName: "watchtower", BindPaths: []string{sock}}},
	}
	f.damage(t)

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: "/"}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, f.normal, normalContent, "normal file")
	if sink.contains("skipping bind mount") {
		t.Errorf("bind mount warning for a socket:\n%v", sink.logs)
	}
}

// TestInPlace_ListFailsWithReportedRoot: when Docker reports its volumes root
// but listing the containers fails, it is that root that is left out.
func TestInPlace_ListFailsWithReportedRoot(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	id := f.env.backup(t, f.volRoot, e2eSource)
	f.env.e.dockerVolRoot = filepath.Join(t.TempDir(), "default-volumes")
	f.env.e.docker = &fakeDocker{err: errors.New("docker daemon unreachable"), volumesRoot: f.volRoot}
	f.damage(t)

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: "/"}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, f.normal, normalContent, "normal file")
	assertContent(t, f.dbTxt, damaged, "db.txt under the reported root")
	if want := "so Docker volumes under " + f.volRoot + ", " + f.alias + " were skipped"; !sink.contains(want) {
		t.Errorf("no %q warning in logs:\n%v", want, sink.logs)
	}
}

// TestInPlace_ExcludeWithGlobCharacters: restic reads --exclude values as glob
// patterns; a bind mount named app[1] must exclude exactly that directory,
// not its sibling app1.
func TestInPlace_ExcludeWithGlobCharacters(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	const bind = "/srv/app[1]"
	f.env.e.docker = &fakeDocker{
		volumesRoot: f.volRoot,
		running:     []docker.RunningContainerVolumes{{ContainerName: "web", BindPaths: []string{bind}}},
	}
	inBind := f.env.hostPath(bind + "/state.txt")
	sibling := f.env.hostPath("/srv/app1/state.txt")
	writeFile(t, inBind, volContent)
	writeFile(t, sibling, volContent)
	id := f.env.backup(t, "/srv")
	writeFile(t, inBind, damaged)
	writeFile(t, sibling, damaged)

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: "/"}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, inBind, damaged, "file in running container's bind mount app[1]")
	assertContent(t, sibling, volContent, "file in sibling app1")
}

// TestInPlace_SelfNeedsHostRootMount: with network_mode container:X the
// agent's /etc/hostname comes from X, so the self ID points at X. A container
// is only taken as the agent when it mounts something at the host root.
func TestInPlace_SelfNeedsHostRootMount(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	id := f.env.backup(t, e2eSource)
	const self = "0123456789ab"
	f.env.e.selfContainerID = self
	f.env.e.docker = &fakeDocker{
		volumesRoot: f.volRoot,
		running: []docker.RunningContainerVolumes{{ContainerID: self + "cdef0123456789", ContainerName: "vpn",
			BindPaths: []string{"/home/u"}, MountDestinations: []string{"/config"}}},
	}
	f.damage(t)

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: "/"}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, f.normal, damaged, "normal file under vpn's bind mount")
	if want := `skipping bind mount "/home/u" for in-place restore: container "vpn" is running`; !sink.contains(want) {
		t.Errorf("no %q warning in logs:\n%v", want, sink.logs)
	}
}

// TestInPlace_BindAboveDataRootExcluded: on a NAS with data-root
// /mnt/storage/docker, a running container (Syncthing, Samba) binding
// /mnt/storage is protected; only binds of exactly the data root or the
// volumes root are ignored.
func TestInPlace_BindAboveDataRootExcluded(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	share := f.env.hostPath("/mnt/storage/share/f.txt")
	writeFile(t, share, volContent)
	id := f.env.backup(t, "/mnt/storage", e2eSource)
	f.env.e.docker = &fakeDocker{
		volumesRoot: "/mnt/storage/docker/volumes",
		running: []docker.RunningContainerVolumes{
			{ContainerName: "syncthing", BindPaths: []string{"/mnt/storage"}},
			{ContainerName: "cadvisor", BindPaths: []string{"/mnt/storage/docker"}},
			{ContainerName: "portainer_agent", BindPaths: []string{"/mnt/storage/docker/volumes"}},
		},
	}
	f.damage(t)
	writeFile(t, share, damaged)

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{ResticSnapshotID: id, TargetPath: "/"}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, f.normal, normalContent, "normal file")
	assertContent(t, share, damaged, "file under syncthing's bind mount")
	if want := `skipping bind mount "/mnt/storage" for in-place restore: container "syncthing" is running`; !sink.contains(want) {
		t.Errorf("no %q warning in logs:\n%v", want, sink.logs)
	}
	for _, c := range []string{"cadvisor", "portainer_agent"} {
		if sink.contains(`container "` + c + `"`) {
			t.Errorf("warning for %s, whose bind is the data or volumes root:\n%v", c, sink.logs)
		}
	}
}

// TestInPlace_IncludeWithGlobCharacters: restic reads --include values as
// glob patterns too; including app[1] must restore exactly that directory.
func TestInPlace_IncludeWithGlobCharacters(t *testing.T) {
	t.Parallel()
	f := newVolumeFixture(t)
	inBracket := f.env.hostPath("/srv/app[1]/state.txt")
	sibling := f.env.hostPath("/srv/app1/state.txt")
	writeFile(t, inBracket, volContent)
	writeFile(t, sibling, volContent)
	id := f.env.backup(t, "/srv")
	writeFile(t, inBracket, damaged)
	writeFile(t, sibling, damaged)

	sink := &recordingSink{}
	r := f.env.restore(t, context.Background(), restorePayload{
		ResticSnapshotID: id, TargetPath: "/", IncludePaths: []string{filepath.Dir(inBracket)},
	}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, inBracket, volContent, "included app[1]")
	assertContent(t, sibling, damaged, "sibling app1 (not included)")
}
