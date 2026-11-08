// Package docker provides read-only discovery of Docker volumes via the Docker
// daemon socket. It is used by the executor to resolve backup sources of the
// form "docker-volume://<volume-name>" into the actual mountpoint path on the
// host filesystem.
//
// The Docker socket is mounted read-only in the agent container — this package
// never writes to the daemon, it only issues List, Inspect and Info calls.
//
// If Docker is not available on the host (socket missing or daemon not running),
// all methods return ErrDockerUnavailable so the executor can skip volume
// discovery gracefully instead of failing the entire backup job.
package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"runtime"
	"strings"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/mount"
	dockerclient "github.com/moby/moby/client"
)

// ErrDockerUnavailable is returned when the Docker daemon cannot be reached.
// Callers should treat this as a non-fatal condition when Docker support is
// optional for the current policy.
var ErrDockerUnavailable = errors.New("docker: daemon unavailable")

// ErrVolumeNotFound is returned when a requested volume does not exist.
var ErrVolumeNotFound = errors.New("docker: volume not found")

// VolumeInfo holds the metadata of a Docker volume relevant to backup.
type VolumeInfo struct {
	// Name is the Docker volume name (e.g. "myapp_postgres_data").
	Name string
	// Mountpoint is the absolute path on the host where the volume data lives.
	// This is the path passed to restic as a backup source.
	Mountpoint string
	// Driver is the volume driver (e.g. "local", "rexray/s3").
	// Non-local drivers may not have an accessible host mountpoint.
	Driver string
	// Labels are the Docker labels attached to the volume.
	Labels map[string]string
}

// Client wraps the Docker SDK client and provides volume discovery methods.
// Create instances with NewClient.
type Client struct {
	docker *dockerclient.Client
}

// NewClient creates a Docker Client connected to the socket at socketPath.
// Use the empty string to fall back to the Docker SDK default
// (DOCKER_HOST env var, or /var/run/docker.sock on Linux/macOS,
// //./pipe/docker_engine on Windows).
//
// Returns ErrDockerUnavailable if the socket does not exist or the daemon
// is not responding.
func NewClient(socketPath string) (*Client, error) {
	var opts []dockerclient.Opt

	if socketPath != "" {
		opts = append(opts, dockerclient.WithHost(hostURI(socketPath)))
	} else if runtime.GOOS == "windows" {
		opts = append(opts, dockerclient.WithHost("npipe:////./pipe/docker_engine"))
	}

	dc, err := dockerclient.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrDockerUnavailable, err)
	}

	return &Client{docker: dc}, nil
}

// Ping checks that the Docker daemon is reachable.
// Call this at startup to detect early whether Docker is available.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.docker.Ping(ctx, dockerclient.PingOptions{})
	if err != nil {
		return fmt.Errorf("%w: %s", ErrDockerUnavailable, err)
	}
	return nil
}

// ListVolumes returns all Docker volumes visible to the daemon.
// An optional label filter can be passed to restrict results
// (e.g. "com.example.backup=true"). Pass an empty string for no filter.
//
// Returns ErrDockerUnavailable if the daemon is not reachable.
func (c *Client) ListVolumes(ctx context.Context, labelFilter string) ([]VolumeInfo, error) {
	opts := dockerclient.VolumeListOptions{}
	if labelFilter != "" {
		opts.Filters = make(dockerclient.Filters).Add("label", labelFilter)
	}

	list, err := c.docker.VolumeList(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrDockerUnavailable, err)
	}

	volumes := make([]VolumeInfo, 0, len(list.Items))
	for _, v := range list.Items {
		volumes = append(volumes, VolumeInfo{
			Name:       v.Name,
			Mountpoint: v.Mountpoint,
			Driver:     v.Driver,
			Labels:     v.Labels,
		})
	}
	return volumes, nil
}

// InspectVolume returns the metadata of a single volume by name.
// Returns ErrVolumeNotFound if the volume does not exist.
func (c *Client) InspectVolume(ctx context.Context, name string) (*VolumeInfo, error) {
	res, err := c.docker.VolumeInspect(ctx, name, dockerclient.VolumeInspectOptions{})
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil, ErrVolumeNotFound
		}
		return nil, fmt.Errorf("%w: %s", ErrDockerUnavailable, err)
	}

	v := res.Volume
	return &VolumeInfo{
		Name:       v.Name,
		Mountpoint: v.Mountpoint,
		Driver:     v.Driver,
		Labels:     v.Labels,
	}, nil
}

// RunningContainerVolumes holds the host paths a single running container writes to.
type RunningContainerVolumes struct {
	// ContainerID is the full container ID.
	ContainerID string
	// ContainerName is the human-readable name of the container (without leading slash).
	ContainerName string
	// VolumePaths are the host-side mountpoints of the named volumes used by this
	// container (e.g. /var/lib/docker/volumes/myapp_data/_data).
	VolumePaths []string
	// BindPaths are the host-side sources of the container's writable bind
	// mounts (e.g. /srv/myapp/data). A bind source of "/" is left out: that
	// is how the agent itself mounts the host (/:/hostfs), and it would cover
	// every path.
	BindPaths []string
	// MountDestinations are the in-container paths of all its mounts, used
	// to tell the agent's own container (which mounts the host root) apart.
	MountDestinations []string
}

// ListRunningContainerVolumes returns the host paths currently running
// containers write to: their named-volume mountpoints and writable bind-mount
// sources. It is used by the executor to determine which paths cannot be
// restored in-place without first stopping the container.
//
// Read-only bind mounts and other mount types are skipped. If a volume's
// Source path is empty in the ContainerList response (the daemon may omit
// it), the path is resolved via VolumeInspect.
func (c *Client) ListRunningContainerVolumes(ctx context.Context) ([]RunningContainerVolumes, error) {
	containers, err := c.docker.ContainerList(ctx, dockerclient.ContainerListOptions{All: false})
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrDockerUnavailable, err)
	}

	var result []RunningContainerVolumes
	for _, ctr := range containers.Items {
		name := ctr.ID[:12]
		if len(ctr.Names) > 0 {
			name = strings.TrimPrefix(ctr.Names[0], "/")
		}

		var paths, binds, dests []string
		for _, m := range ctr.Mounts {
			dests = append(dests, m.Destination)
			if m.Type == mount.TypeBind {
				if m.RW && m.Source != "" && m.Source != "/" {
					binds = append(binds, m.Source)
				}
				continue
			}
			if m.Type != mount.TypeVolume {
				continue
			}
			src := m.Source
			if src == "" && m.Name != "" {
				// Source not populated by ContainerList — resolve via VolumeInspect.
				info, err := c.InspectVolume(ctx, m.Name)
				if err == nil {
					src = info.Mountpoint
				}
			}
			if src != "" {
				paths = append(paths, src)
			}
		}
		if len(paths) > 0 || len(binds) > 0 {
			result = append(result, RunningContainerVolumes{
				ContainerID:   ctr.ID,
				ContainerName: name,
				VolumePaths:   paths,
				BindPaths:     binds,

				MountDestinations: dests,
			})
		}
	}
	return result, nil
}

// VolumesRoot returns the directory where the daemon keeps named volumes:
// its data root (DockerRootDir, /var/lib/docker unless configured otherwise)
// followed by "/volumes".
func (c *Client) VolumesRoot(ctx context.Context) (string, error) {
	res, err := c.docker.Info(ctx, dockerclient.InfoOptions{})
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrDockerUnavailable, err)
	}
	if res.Info.DockerRootDir == "" {
		return "", errors.New("docker: daemon did not report its data root")
	}
	return path.Join(res.Info.DockerRootDir, "volumes"), nil
}

// SelfContainerID returns the ID of the container the agent runs in, or ""
// when it cannot tell (e.g. a native agent). It reads /proc/self/mountinfo,
// falling back to the hostname, which Docker sets to the short container ID
// unless configured otherwise. The result may be that short ID: compare it
// as a prefix of full IDs.
func SelfContainerID() string {
	mountinfo, _ := os.ReadFile("/proc/self/mountinfo")
	hostname, _ := os.Hostname()
	return selfContainerID(string(mountinfo), hostname)
}

// containerIDRe matches a container's own directory in a mount source, as
// used by Docker (…/docker/containers/<id>/) and Podman
// (…/overlay-containers/<id>/userdata/).
var containerIDRe = regexp.MustCompile(`containers/([0-9a-f]{64})/`)

// shortIDRe matches a short container ID, Docker's default container hostname.
var shortIDRe = regexp.MustCompile(`^[0-9a-f]{12}$`)

// selfContainerID finds the agent's container ID in mountinfo: the runtime
// bind-mounts the container's own /etc/hostname, /etc/hosts and
// /etc/resolv.conf from its per-container directory.
func selfContainerID(mountinfo, hostname string) string {
	for _, line := range strings.Split(mountinfo, "\n") {
		// Fields: id parent major:minor root mountpoint ...
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		switch f[4] {
		case "/etc/hostname", "/etc/hosts", "/etc/resolv.conf":
			if m := containerIDRe.FindStringSubmatch(f[3]); m != nil {
				return m[1]
			}
		}
	}
	if shortIDRe.MatchString(hostname) {
		return hostname
	}
	return ""
}

// Close releases the underlying Docker client resources.
func (c *Client) Close() error {
	return c.docker.Close()
}

// hostURI converts a raw socket/pipe path to the full URI expected by the
// Docker SDK. If the path already contains a scheme (e.g. "unix://",
// "npipe://", "tcp://") it is returned unchanged. Otherwise the correct
// scheme is prepended based on the current OS.
func hostURI(path string) string {
	if strings.Contains(path, "://") {
		return path
	}
	if runtime.GOOS == "windows" {
		return "npipe://" + path
	}
	return "unix://" + path
}
