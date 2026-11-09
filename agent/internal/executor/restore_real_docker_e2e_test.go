package executor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/arkeep-io/arkeep/agent/internal/docker"
)

// e2eBusybox is pinned by digest (ADR 0013): a moving tag must not change
// what the test runs.
const e2eBusybox = "busybox@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec" // 1.37.0

// TestInPlace_RealDocker_RunningContainerVolumeSkipped runs against the real
// Docker Engine: an in-place restore must leave the volume of a running
// container alone. It writes into /var/lib/docker/volumes, so it needs root
// and skips otherwise (including in CI). Run it as root without root-owned
// files in the Go caches: build as the user, run the binary as root:
//
//	cd agent/internal/executor && go test -c -o /tmp/executor.test . &&
//	  sudo /tmp/executor.test -test.run TestInPlace_RealDocker -test.v -test.count=1 -test.timeout=10m
//
// DOCKER_HOST / the Docker context must match if non-default.
func TestInPlace_RealDocker_RunningContainerVolumeSkipped(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root: it writes into Docker's volumes root (/var/lib/docker/volumes)")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not on PATH")
	}
	client, err := docker.NewClient("")
	if err != nil {
		t.Skipf("docker client unavailable: %v", err)
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}

	dockerCLI := func(args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	}
	if out, err := dockerCLI("pull", e2eBusybox); err != nil {
		t.Skipf("cannot pull %s: %v\n%s", e2eBusybox, err, out)
	}

	var rnd [4]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		t.Fatal(err)
	}
	name := "arkeep-e2e-" + hex.EncodeToString(rnd[:])
	t.Cleanup(func() {
		// Own context: ctx may be done when the test fails by timeout.
		rm := func(args ...string) {
			c, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if out, err := exec.CommandContext(c, "docker", args...).CombinedOutput(); err != nil {
				t.Logf("cleanup docker %v: %v\n%s", args, err, out)
			}
		}
		rm("rm", "-f", name)
		rm("volume", "rm", "-f", name)
	})

	if out, err := dockerCLI("volume", "create", name); err != nil {
		t.Fatalf("volume create: %v\n%s", err, out)
	}
	if out, err := dockerCLI("run", "-d", "--name", name, "-v", name+":/data", e2eBusybox, "sleep", "300"); err != nil {
		t.Fatalf("run container: %v\n%s", err, out)
	}

	info, err := client.InspectVolume(ctx, name)
	if err != nil {
		t.Fatalf("inspect volume: %v", err)
	}
	if !pathWithin(info.Mountpoint, defaultDockerVolRoot) {
		t.Skipf("volume mountpoint %q is not under %s (Docker Desktop, custom data-root or rootless Docker)", info.Mountpoint, defaultDockerVolRoot)
	}
	if _, err := os.Stat(info.Mountpoint); err != nil {
		t.Skipf("volume mountpoint %q not accessible from this host: %v", info.Mountpoint, err)
	}
	dbTxt := filepath.Join(info.Mountpoint, "db.txt")
	writeFile(t, dbTxt, volContent)

	env := newE2EEnv(t, false)
	env.e.docker = client
	env.e.dockerVolRoot = defaultDockerVolRoot
	id := env.backup(t, "docker-volume://"+name)

	writeFile(t, dbTxt, damaged)
	sink := &recordingSink{}
	r := env.restore(t, ctx, restorePayload{ResticSnapshotID: id, TargetPath: "/"}, sink)
	mustSucceed(t, r, sink)
	assertContent(t, dbTxt, damaged, "db.txt of running container's volume")
	if !sink.contains(fmt.Sprintf("container %q is running", name)) {
		t.Errorf("no running-container warning in logs:\n%v", sink.logs)
	}
}
