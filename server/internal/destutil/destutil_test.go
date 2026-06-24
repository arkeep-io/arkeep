package destutil

import (
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
			name:   "sftp empty port omitted",
			dType:  "sftp",
			config: `{"host":"h","user":"u","path":"/p","port":""}`,
			want:   "sftp:u@h:/p",
		},
		{
			name:   "sftp default port 22 omitted",
			dType:  "sftp",
			config: `{"host":"h","user":"u","path":"/p","port":"22"}`,
			want:   "sftp:u@h:/p",
		},
		{
			name:   "sftp custom port included",
			dType:  "sftp",
			config: `{"host":"h","user":"u","path":"/p","port":"2222"}`,
			want:   "sftp:u@h:2222:/p",
		},
		{
			name:   "sftp no port key",
			dType:  "sftp",
			config: `{"host":"h","user":"u","path":"/p"}`,
			want:   "sftp:u@h:/p",
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
