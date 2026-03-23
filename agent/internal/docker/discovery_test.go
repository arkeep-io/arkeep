package docker

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestHostURI_PreservesExistingScheme(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"tcp://1.2.3.4:2376", "tcp://1.2.3.4:2376"},
		{"unix:///var/run/docker.sock", "unix:///var/run/docker.sock"},
		{"npipe:////./pipe/docker_engine", "npipe:////./pipe/docker_engine"},
		{"ssh://user@host:22", "ssh://user@host:22"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := hostURI(tc.input)
			if got != tc.want {
				t.Errorf("hostURI(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestHostURI_UnixDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket paths not applicable on Windows")
	}
	got := hostURI("/var/run/docker.sock")
	want := "unix:///var/run/docker.sock"
	if got != want {
		t.Errorf("hostURI(%q) = %q, want %q", "/var/run/docker.sock", got, want)
	}
}

func TestHostURI_WindowsNamedPipe(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("named pipe paths only applicable on Windows")
	}
	got := hostURI("//./pipe/docker_engine")
	want := "npipe:////./pipe/docker_engine"
	if got != want {
		t.Errorf("hostURI(%q) = %q, want %q", "//./pipe/docker_engine", got, want)
	}
}

func TestNewClient_DockerUnavailable(t *testing.T) {
	// Use a path inside t.TempDir() — the file does not exist as a socket,
	// so any connection attempt will fail.
	fakePath := filepath.Join(t.TempDir(), "fake_docker.sock")

	c, err := NewClient(fakePath)
	if err != nil {
		// Some SDK versions reject the host URI at construction time.
		if !errors.Is(err, ErrDockerUnavailable) {
			t.Fatalf("NewClient: expected ErrDockerUnavailable, got: %v", err)
		}
		return
	}
	defer c.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = c.Ping(ctx)
	if err == nil {
		t.Fatal("Ping: expected error for non-existent socket, got nil")
	}
	if !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("Ping: expected ErrDockerUnavailable, got: %v", err)
	}
}
