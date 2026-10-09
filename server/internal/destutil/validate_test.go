package destutil

import (
	"errors"
	"testing"
)

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name     string
		destType string
		config   string
		valid    bool
	}{
		{"local ok", "local", `{"path":"/backups"}`, true},
		{"local missing path", "local", `{}`, false},
		{"local blank path", "local", `{"path":"  "}`, false},
		{"not an object", "local", `[]`, false},
		{"null", "local", `null`, false},
		{"non-string value", "local", `{"path":1}`, false},
		{"s3 ok", "s3", `{"bucket":"b","endpoint":"s3.example.com","region":"","prefix":""}`, true},
		{"s3 missing endpoint", "s3", `{"bucket":"b"}`, false},
		{"s3 missing bucket", "s3", `{"endpoint":"s3.example.com"}`, false},
		{"sftp ok", "sftp", `{"host":"h","user":"u","path":"/p","port":"2222"}`, true},
		{"sftp blank port", "sftp", `{"host":"h","user":"u","path":"/p","port":""}`, true},
		{"sftp bad port", "sftp", `{"host":"h","user":"u","path":"/p","port":"22x"}`, false},
		{"sftp port out of range", "sftp", `{"host":"h","user":"u","path":"/p","port":"70000"}`, false},
		{"sftp missing user", "sftp", `{"host":"h","path":"/p"}`, false},
		{"rest ok", "rest", `{"url":"https://rest.example.com:8000/repo"}`, true},
		{"rest not a URL", "rest", `{"url":"rest.example.com"}`, false},
		{"rest other scheme", "rest", `{"url":"ftp://rest.example.com"}`, false},
		{"rclone ok", "rclone", `{"remote":"gdrive","path":"backups"}`, true},
		{"rclone connection string", "rclone", `{"remote":":sftp,host=x:","path":"p"}`, false},
		{"rclone missing path", "rclone", `{"remote":"gdrive"}`, false},
		{"unknown type", "ftp", `{}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateConfig(tt.destType, tt.config); (err == nil) != tt.valid {
				t.Errorf("ValidateConfig(%q, %s) error = %v, want valid=%v", tt.destType, tt.config, err, tt.valid)
			}
		})
	}
}

func TestValidateCredentials(t *testing.T) {
	tests := []struct {
		name     string
		destType string
		creds    string
		valid    bool
	}{
		{"local empty", "local", ``, true},
		{"s3 ok", "s3", `{"access_key":"a","secret_key":"s"}`, true},
		{"s3 missing secret", "s3", `{"access_key":"a","secret_key":""}`, false},
		{"s3 empty", "s3", `{}`, false},
		{"sftp password only", "sftp", `{"password":"p","private_key":""}`, true},
		{"rest no auth", "rest", `{"user":"","password":""}`, true},
		{"rclone blank", "rclone", `{"x":""}`, true},
		{"not an object", "sftp", `"secret"`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateCredentials(tt.destType, tt.creds); (err == nil) != tt.valid {
				t.Errorf("ValidateCredentials(%q, %s) error = %v, want valid=%v", tt.destType, tt.creds, err, tt.valid)
			}
		})
	}

	if err := ValidateCredentials("rclone", `{"RESTIC_PASSWORD_COMMAND":"id"}`); !errors.Is(err, ErrRcloneCredentials) {
		t.Errorf("rclone with credentials: error = %v, want ErrRcloneCredentials", err)
	}
}
