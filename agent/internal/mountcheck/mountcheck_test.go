package mountcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// containerMountinfo is the /proc/self/mountinfo of an agent container with
// the mounts the tests need: the overlay root, a bind mount, a named volume,
// a tmpfs, the read-only host root with a writable share under it (Unraid),
// a mount point with a space, and /over mounted twice (the later one wins).
const containerMountinfo = `1012 925 0:85 / / rw,relatime master:1 - overlay overlay rw,lowerdir=/var/lib/docker/overlay2/l/A,upperdir=/var/lib/docker/overlay2/x/diff
1013 1012 0:88 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw
1020 1012 0:90 / /dev rw,nosuid - tmpfs tmpfs rw,size=65536k,mode=755
1030 1012 8:1 /srv/backups /backups rw,relatime - ext4 /dev/sda1 rw
1031 1012 8:1 /var/lib/docker/volumes/agent/_data /data rw,relatime - ext4 /dev/sda1 rw
1032 1012 0:95 / /scratch rw,nosuid,nodev - tmpfs tmpfs rw
1033 1012 8:1 / /hostfs ro,relatime master:2 - ext4 /dev/sda1 ro
1034 1033 0:50 / /hostfs/mnt/user rw shared:3 master:4 - fuse.shfs shfs rw
1035 1012 8:1 /srv/my\040backups /my\040backups rw - ext4 /dev/sda1 rw
1036 1012 8:1 /srv/over /over rw - ext4 /dev/sda1 rw
1037 1036 0:96 / /over rw - tmpfs tmpfs rw
1038 1012 0:97 / /ram rw - ramfs ramfs rw
`

func TestParseMountInfo(t *testing.T) {
	mounts, err := ParseMountInfo(strings.NewReader(containerMountinfo))
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 12 {
		t.Fatalf("got %d mounts, want 12", len(mounts))
	}
	tests := []struct {
		i      int
		point  string
		fsType string
	}{
		{0, "/", "overlay"},
		{3, "/backups", "ext4"},
		{7, "/hostfs/mnt/user", "fuse.shfs"}, // optional fields before "-"
		{8, "/my backups", "ext4"},           // \040 decoded
	}
	for _, tt := range tests {
		if got := mounts[tt.i]; got.Point != tt.point || got.FSType != tt.fsType {
			t.Errorf("mounts[%d] = %+v, want {%s %s}", tt.i, got, tt.point, tt.fsType)
		}
	}
}

func TestParseMountInfo_Malformed(t *testing.T) {
	for _, in := range []string{
		"1012 925 0:85 / / rw,relatime overlay overlay rw\n", // no separator
		"1012 925 0:85 / / rw - \n",                          // no fstype
		"1012 925 0:85\n",                                    // truncated
	} {
		if _, err := ParseMountInfo(strings.NewReader(in)); err == nil {
			t.Errorf("ParseMountInfo(%q) succeeded, want an error", in)
		}
	}
}

func TestEphemeralKind(t *testing.T) {
	mounts, err := ParseMountInfo(strings.NewReader(containerMountinfo))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		path      string
		ephemeral bool
	}{
		{"container layer", "/backups-not-mounted/repo", true},
		{"container root itself", "/", true},
		{"bind mount", "/backups/repo", false},
		{"bind mount point", "/backups", false},
		{"named volume", "/data/repo", false},
		{"tmpfs", "/scratch/repo", true},
		{"ramfs", "/ram/repo", true},
		{"prefix is not a parent", "/backupsX/repo", true},
		{"host root read-only mount", "/hostfs/srv/repo", false},
		{"writable share under host root", "/hostfs/mnt/user/Arkeep", false},
		{"mount point with a space", "/my backups/repo", false},
		{"later mount on the same point wins", "/over/repo", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind := ephemeralKind(mounts, tt.path)
			if (kind != "") != tt.ephemeral {
				t.Errorf("ephemeralKind(%q) = %q, want ephemeral=%v", tt.path, kind, tt.ephemeral)
			}
		})
	}
}

func TestEphemeralKind_NoMatchingMount(t *testing.T) {
	// Without a root mount nothing is known about the path: not ephemeral.
	mounts := []Mount{{Point: "/backups", FSType: "ext4"}}
	if kind := ephemeralKind(mounts, "/elsewhere/repo"); kind != "" {
		t.Errorf("ephemeralKind = %q, want \"\"", kind)
	}
}

func TestExistingAncestor(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	// dir itself may sit behind a symlink (macOS /var → /private/var).
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	realReal := filepath.Join(realDir, "real")

	tests := []struct {
		name string
		path string
		want string
	}{
		{"existing directory", real, realReal},
		{"missing leaf", filepath.Join(real, "repo"), realReal},
		{"missing chain", filepath.Join(real, "a", "b", "c"), realReal},
		{"through a symlink", filepath.Join(link, "repo"), realReal},
		{"not clean", real + "/./x/../repo", realReal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := existingAncestor(tt.path); got != tt.want {
				t.Errorf("existingAncestor(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestEphemeral(t *testing.T) {
	dir := t.TempDir()
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	mounted := filepath.Join(realDir, "mounted")
	if err := os.Mkdir(mounted, 0o750); err != nil {
		t.Fatal(err)
	}
	mountinfo := filepath.Join(dir, "mountinfo")
	content := "1 0 0:1 / / rw - overlay overlay rw\n" +
		"2 1 8:1 /srv " + mounted + " rw - ext4 /dev/sda1 rw\n"
	if err := os.WriteFile(mountinfo, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	kind, err := Ephemeral(mountinfo, filepath.Join(mounted, "repo"))
	if err != nil || kind != "" {
		t.Errorf("repository under the bind mount: kind=%q err=%v, want not ephemeral", kind, err)
	}
	kind, err = Ephemeral(mountinfo, filepath.Join(realDir, "not-mounted", "repo"))
	if err != nil || kind == "" {
		t.Errorf("repository on the overlay: kind=%q err=%v, want ephemeral", kind, err)
	}
	if _, err := Ephemeral(filepath.Join(dir, "missing"), mounted); err == nil {
		t.Error("unreadable mountinfo: want an error")
	}
}
