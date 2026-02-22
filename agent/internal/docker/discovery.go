// Package docker provides read-only discovery of Docker volumes via the Docker
// daemon socket. It is used by the executor to resolve backup sources of the
// form "docker-volume://<volume-name>" into the actual mountpoint path on the
// host filesystem.
//
// The Docker socket is mounted read-only in the agent container — this package
// never writes to the daemon, it only issues List and Inspect calls.
//
// If Docker is not available on the host (socket missing or daemon not running),
// all methods return ErrDockerUnavailable so the executor can skip volume
// discovery gracefully instead of failing the entire backup job.
package docker

import (
	"context"
	"errors"
	"fmt"

	dockerclient "github.com/docker/docker/client"
	volumetypes "github.com/docker/docker/api/types/volume"
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
	opts := []dockerclient.Opt{
		dockerclient.WithAPIVersionNegotiation(),
	}

	if socketPath != "" {
		opts = append(opts, dockerclient.WithHost("unix://"+socketPath))
	}

	dc, err := dockerclient.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrDockerUnavailable, err)
	}

	return &Client{docker: dc}, nil
}

// Ping checks that the Docker daemon is reachable.
// Call this at startup to detect early whether Docker is available.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.docker.Ping(ctx)
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
	opts := volumetypes.ListOptions{}
	if labelFilter != "" {
		opts.Filters.Add("label", labelFilter)
	}

	list, err := c.docker.VolumeList(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrDockerUnavailable, err)
	}

	volumes := make([]VolumeInfo, 0, len(list.Volumes))
	for _, v := range list.Volumes {
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
	v, err := c.docker.VolumeInspect(ctx, name)
	if err != nil {
		if dockerclient.IsErrNotFound(err) {
			return nil, ErrVolumeNotFound
		}
		return nil, fmt.Errorf("%w: %s", ErrDockerUnavailable, err)
	}

	return &VolumeInfo{
		Name:       v.Name,
		Mountpoint: v.Mountpoint,
		Driver:     v.Driver,
		Labels:     v.Labels,
	}, nil
}

// Close releases the underlying Docker client resources.
func (c *Client) Close() error {
	return c.docker.Close()
}