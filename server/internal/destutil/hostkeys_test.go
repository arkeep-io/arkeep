package destutil

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// newHostKey returns a fresh ed25519 SSH public key and its authorized_keys
// form ("ssh-ed25519 AAAA…", no trailing newline).
func newHostKey(t *testing.T) (ssh.PublicKey, string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("NewPublicKey: %v", err)
	}
	return key, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}

func TestParseHostKeys(t *testing.T) {
	key, line := newHostKey(t)
	other, otherLine := newHostKey(t)

	tests := []struct {
		name  string
		text  string
		want  []ssh.PublicKey
		valid bool
	}{
		{"ssh-keyscan output", "# h.example.com:22 SSH-2.0-OpenSSH_9.6\nh.example.com " + line + "\n", []ssh.PublicKey{key}, true},
		{"non-default port", "[h.example.com]:2222 " + line, []ssh.PublicKey{key}, true},
		{"hashed host", "|1|c2FsdA==|aGFzaA== " + line, []ssh.PublicKey{key}, true},
		{"bare key", line, []ssh.PublicKey{key}, true},
		{"several keys", "h " + line + "\n\nh " + otherLine + "\r\n", []ssh.PublicKey{key, other}, true},
		{"empty", "  \n# only a comment\n", nil, false},
		{"garbage", "h ssh-ed25519 not-base64!", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseHostKeys(tt.text)
			if (err == nil) != tt.valid {
				t.Fatalf("ParseHostKeys error = %v, want valid=%v", err, tt.valid)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ParseHostKeys = %d keys, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if string(got[i].Marshal()) != string(tt.want[i].Marshal()) {
					t.Errorf("key %d differs", i)
				}
			}
		})
	}
}

func TestValidateConfigSFTPHostKey(t *testing.T) {
	_, line := newHostKey(t)
	if err := ValidateConfig("sftp", `{"host":"h","user":"u","path":"/p","host_key":"h `+line+`"}`); err != nil {
		t.Errorf("valid host key refused: %v", err)
	}
	if err := ValidateConfig("sftp", `{"host":"h","user":"u","path":"/p","host_key":"not a key"}`); err == nil {
		t.Error("invalid host key accepted")
	}
}

// TestBuildEnvSFTPKnownHosts: the known_hosts content sent to the agent names
// the destination's own host and port, whatever host the pasted line had, so
// rclone matches it.
func TestBuildEnvSFTPKnownHosts(t *testing.T) {
	key, line := newHostKey(t)
	tests := []struct {
		name, port, wantHost string
	}{
		{"default port", "22", "h.example.com"},
		{"blank port", "", "h.example.com"},
		{"custom port", "2222", "[h.example.com]:2222"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dest := &db.Destination{
				Type:        "sftp",
				Config:      `{"host":"h.example.com","user":"u","path":"/p","port":"` + tt.port + `","host_key":"other.host ` + line + `"}`,
				Credentials: db.EncryptedString(`{"password":"p"}`),
			}
			env := BuildEnv(dest)
			want := knownhosts.Line([]string{tt.wantHost}, key) + "\n"
			if got := env[SFTPKnownHostsEnv]; got != want {
				t.Errorf("%s = %q, want %q", SFTPKnownHostsEnv, got, want)
			}
		})
	}

	// Without a host key nothing is sent: rclone keeps its default.
	env := BuildEnv(&db.Destination{Type: "sftp", Config: `{"host":"h","user":"u","path":"/p"}`})
	if _, ok := env[SFTPKnownHostsEnv]; ok {
		t.Errorf("%s set without a host key", SFTPKnownHostsEnv)
	}
}
