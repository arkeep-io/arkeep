package destutil

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"testing"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

func TestBuildRepoURL(t *testing.T) {
	tests := []struct {
		name    string
		dType   string
		config  string
		want    string
	}{
		{
			name:   "sftp routed through rclone remote",
			dType:  "sftp",
			config: `{"host":"h","user":"u","path":"/p","port":""}`,
			want:   "rclone:arkeepsftp:/p",
		},
		{
			name:   "sftp port does not affect repo url",
			dType:  "sftp",
			config: `{"host":"h","user":"u","path":"/p","port":"2222"}`,
			want:   "rclone:arkeepsftp:/p",
		},
		{
			name:   "sftp missing host yields empty",
			dType:  "sftp",
			config: `{"user":"u","path":"/p","port":"22"}`,
			want:   "",
		},
		{
			name:   "local",
			dType:  "local",
			config: `{"path":"/mnt/backups"}`,
			want:   "/mnt/backups",
		},
		{
			name:   "s3",
			dType:  "s3",
			config: `{"bucket":"b","endpoint":"s3.example.com","path":"/x"}`,
			want:   "s3:s3.example.com/b/x",
		},
		{
			name:   "rest",
			dType:  "rest",
			config: `{"url":"https://rest.example.com/repo"}`,
			want:   "rest:https://rest.example.com/repo",
		},
		{
			name:   "rclone",
			dType:  "rclone",
			config: `{"remote":"myremote:bucket"}`,
			want:   "rclone:myremote:bucket",
		},
		{
			name:   "rclone with path, remote has trailing colon",
			dType:  "rclone",
			config: `{"remote":"pCloudDrive:","path":"Arkeep"}`,
			want:   "rclone:pCloudDrive:Arkeep",
		},
		{
			name:   "rclone with path, remote without colon",
			dType:  "rclone",
			config: `{"remote":"pCloudDrive","path":"Arkeep"}`,
			want:   "rclone:pCloudDrive:Arkeep",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dest := &db.Destination{Type: tt.dType, Config: tt.config}
			if got := BuildRepoURL(dest); got != tt.want {
				t.Errorf("BuildRepoURL(%s, %s) = %q, want %q", tt.dType, tt.config, got, tt.want)
			}
		})
	}
}

func TestBuildEnvSFTP(t *testing.T) {
	const key = "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\ndef\n-----END OPENSSH PRIVATE KEY-----\n"
	dest := &db.Destination{
		Type:        "sftp",
		Config:      `{"host":"h.example.com","user":"backup","path":"/srv/restic","port":"2222"}`,
		Credentials: db.EncryptedString(`{"password":"s3cret","private_key":"` + escapeJSON(key) + `"}`),
	}

	env := BuildEnv(dest)

	const p = "RCLONE_CONFIG_ARKEEPSFTP_"
	if env[p+"TYPE"] != "sftp" {
		t.Errorf("%sTYPE = %q, want sftp", p, env[p+"TYPE"])
	}
	if env[p+"HOST"] != "h.example.com" {
		t.Errorf("%sHOST = %q", p, env[p+"HOST"])
	}
	if env[p+"USER"] != "backup" {
		t.Errorf("%sUSER = %q", p, env[p+"USER"])
	}
	if env[p+"PORT"] != "2222" {
		t.Errorf("%sPORT = %q, want 2222", p, env[p+"PORT"])
	}
	// key_pem must be single-line with literal \n escapes (no real newlines).
	keyPem := env[p+"KEY_PEM"]
	if keyPem == "" {
		t.Fatal("KEY_PEM not set")
	}
	for _, r := range keyPem {
		if r == '\n' || r == '\r' {
			t.Errorf("KEY_PEM contains a raw newline; must use literal \\n")
			break
		}
	}
	if want := escapeNewlines(key); keyPem != want {
		t.Errorf("KEY_PEM = %q, want %q", keyPem, want)
	}
	// Password must be obscured (not stored plaintext) and reveal back correctly.
	pass := env[p+"PASS"]
	if pass == "" || pass == "s3cret" {
		t.Fatalf("PASS not obscured: %q", pass)
	}
	if got := reveal(t, pass); got != "s3cret" {
		t.Errorf("reveal(PASS) = %q, want s3cret", got)
	}
}

func TestBuildEnvSFTPNoPortNoCreds(t *testing.T) {
	dest := &db.Destination{
		Type:   "sftp",
		Config: `{"host":"h","user":"u","path":"/p","port":"22"}`,
	}
	env := BuildEnv(dest)
	const p = "RCLONE_CONFIG_ARKEEPSFTP_"
	if env[p+"TYPE"] != "sftp" || env[p+"HOST"] != "h" || env[p+"USER"] != "u" {
		t.Errorf("connection env not built: %+v", env)
	}
	if _, ok := env[p+"PORT"]; ok {
		t.Errorf("default port 22 should be omitted, got %q", env[p+"PORT"])
	}
	if _, ok := env[p+"KEY_PEM"]; ok {
		t.Errorf("KEY_PEM should be absent without credentials")
	}
}

// TestObscureRevealsViaRcloneAlgorithm pins our obscure() to rclone's real
// algorithm: a fixed value produced by `rclone obscure "hunter2"` must reveal
// back to the plaintext using rclone's static key, and our own obscure output
// must round-trip through the same reveal.
func TestObscureRevealsViaRcloneAlgorithm(t *testing.T) {
	// Produced by the embedded rclone binary: `rclone obscure hunter2`.
	const rcloneVector = "GWf-M1dW5jJP-OXXu7XgP2IFQ0uyZfc"
	if got := reveal(t, rcloneVector); got != "hunter2" {
		t.Fatalf("reveal(rclone vector) = %q, want hunter2 — static key/algorithm mismatch", got)
	}

	for _, pw := range []string{"", "p", "correct horse battery staple", "münchen✓"} {
		obscured, err := obscure(pw)
		if err != nil {
			t.Fatalf("obscure(%q) error: %v", pw, err)
		}
		if obscured == pw && pw != "" {
			t.Errorf("obscure(%q) returned plaintext", pw)
		}
		if got := reveal(t, obscured); got != pw {
			t.Errorf("reveal(obscure(%q)) = %q", pw, got)
		}
	}
}

// reveal is the inverse of obscure, used only in tests to validate round-trips.
func reveal(t *testing.T, s string) string {
	t.Helper()
	buf, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("reveal: base64 decode: %v", err)
	}
	if len(buf) < aes.BlockSize {
		t.Fatalf("reveal: input too short")
	}
	block, err := aes.NewCipher(cryptKey)
	if err != nil {
		t.Fatalf("reveal: cipher: %v", err)
	}
	iv := buf[:aes.BlockSize]
	out := make([]byte, len(buf)-aes.BlockSize)
	cipher.NewCTR(block, iv).XORKeyStream(out, buf[aes.BlockSize:])
	return string(out)
}

func escapeNewlines(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\n' {
			out = append(out, '\\', 'n')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// escapeJSON escapes a string for safe embedding inside a JSON string literal
// (newlines and quotes), so credential fixtures can be written inline.
func escapeJSON(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch r {
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		default:
			out = append(out, r)
		}
	}
	return string(out)
}
