package docker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
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

// fakeDaemon serves the Docker API endpoints the client uses, from canned
// JSON, and returns a Client connected to it.
func fakeDaemon(t *testing.T, containersJSON, infoJSON string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			_, _ = w.Write([]byte(containersJSON))
		case strings.HasSuffix(r.URL.Path, "/info"):
			_, _ = w.Write([]byte(infoJSON))
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("Api-Version", "1.47")
			_, _ = w.Write([]byte("OK"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestListRunningContainerVolumes_BindPaths(t *testing.T) {
	c := fakeDaemon(t, `[
		{"Id":"aaaaaaaaaaaaaaaa","Names":["/app"],"Mounts":[
			{"Type":"volume","Name":"app_data","Source":"/var/lib/docker/volumes/app_data/_data","Destination":"/data","RW":true},
			{"Type":"bind","Source":"/srv/app","Destination":"/srv","RW":true},
			{"Type":"bind","Source":"/etc/app.conf","Destination":"/etc/app.conf","RW":false},
			{"Type":"bind","Source":"/","Destination":"/hostfs","RW":true}
		]},
		{"Id":"bbbbbbbbbbbbbbbb","Names":["/web"],"Mounts":[
			{"Type":"bind","Source":"/srv/web","Destination":"/web","RW":true}
		]},
		{"Id":"cccccccccccccccc","Names":["/ro"],"Mounts":[
			{"Type":"bind","Source":"/srv/ro","RW":false}
		]}
	]`, `{}`)

	got, err := c.ListRunningContainerVolumes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []RunningContainerVolumes{
		{ContainerID: "aaaaaaaaaaaaaaaa", ContainerName: "app", VolumePaths: []string{"/var/lib/docker/volumes/app_data/_data"}, BindPaths: []string{"/srv/app"},
			MountDestinations: []string{"/data", "/srv", "/etc/app.conf", "/hostfs"}},
		{ContainerID: "bbbbbbbbbbbbbbbb", ContainerName: "web", BindPaths: []string{"/srv/web"}, MountDestinations: []string{"/web"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListRunningContainerVolumes() = %+v, want %+v", got, want)
	}
}

func TestVolumesRoot(t *testing.T) {
	c := fakeDaemon(t, `[]`, `{"DockerRootDir":"/data/docker"}`)
	got, err := c.VolumesRoot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := "/data/docker/volumes"; got != want {
		t.Errorf("VolumesRoot() = %q, want %q", got, want)
	}

	empty := fakeDaemon(t, `[]`, `{}`)
	if got, err := empty.VolumesRoot(context.Background()); err == nil {
		t.Errorf("VolumesRoot() with no DockerRootDir = %q, want an error", got)
	}
}

func TestSelfContainerID(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tests := []struct {
		name      string
		mountinfo string
		hostname  string
		want      string
	}{
		{
			name: "docker",
			mountinfo: "1 0 0:1 / / rw - overlay overlay rw\n" +
				"2 1 8:1 /var/lib/docker/containers/" + id + "/hostname /etc/hostname rw,relatime - ext4 /dev/sda1 rw\n",
			hostname: "myhost",
			want:     id,
		},
		{
			name:      "rootless docker",
			mountinfo: "2 1 8:1 /home/u/.local/share/docker/containers/" + id + "/resolv.conf /etc/resolv.conf rw - ext4 /dev/sda1 rw\n",
			want:      id,
		},
		{
			name:      "podman",
			mountinfo: "2 1 0:5 /containers/storage/overlay-containers/" + id + "/userdata/hostname /etc/hostname rw - tmpfs tmpfs rw\n",
			want:      id,
		},
		{
			name:      "id elsewhere is not the container's own files",
			mountinfo: "2 1 8:1 /var/lib/docker/containers/" + id + "/x /data rw - ext4 /dev/sda1 rw\n",
			hostname:  "myhost",
			want:      "",
		},
		{name: "hostname fallback", mountinfo: "", hostname: "0123456789ab", want: "0123456789ab"},
		{name: "native host", mountinfo: "1 0 8:1 / / rw - ext4 /dev/sda1 rw\n", hostname: "fileserver", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selfContainerID(tt.mountinfo, tt.hostname); got != tt.want {
				t.Errorf("selfContainerID() = %q, want %q", got, tt.want)
			}
		})
	}
}
