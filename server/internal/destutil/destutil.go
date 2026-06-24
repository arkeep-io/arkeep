// Package destutil provides helpers for building restic repository URLs and
// backend environment variables from db.Destination records. These functions
// are shared between the scheduler (backup dispatch) and the API layer
// (restore dispatch) to ensure consistent URL construction.
package destutil

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// BuildRepoURL constructs the restic repository URL from a destination record.
// The format depends on the destination type and matches what restic expects.
func BuildRepoURL(dest *db.Destination) string {
	switch dest.Type {
	case "local":
		var cfg struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(dest.Config), &cfg); err == nil && cfg.Path != "" {
			return cfg.Path
		}
	case "s3":
		var cfg struct {
			Bucket   string `json:"bucket"`
			Endpoint string `json:"endpoint"`
			Path     string `json:"path"`
		}
		if err := json.Unmarshal([]byte(dest.Config), &cfg); err == nil && cfg.Bucket != "" {
			endpoint := cfg.Endpoint
			if endpoint == "" {
				endpoint = "s3.amazonaws.com"
			}
			path := cfg.Path
			if path == "" {
				path = "/"
			}
			return fmt.Sprintf("s3:%s/%s%s", endpoint, cfg.Bucket, path)
		}
	case "sftp":
		// Port is parsed as a string because the GUI stores it as one in the
		// config JSON; unmarshalling into an int would fail and yield an empty
		// repo URL, silently skipping every SFTP backup.
		var cfg struct {
			Host string `json:"host"`
			User string `json:"user"`
			Path string `json:"path"`
			Port string `json:"port"`
		}
		if err := json.Unmarshal([]byte(dest.Config), &cfg); err == nil && cfg.Host != "" {
			user := ""
			if cfg.User != "" {
				user = cfg.User + "@"
			}
			port := ""
			if cfg.Port != "" && cfg.Port != "22" {
				port = ":" + cfg.Port
			}
			return fmt.Sprintf("sftp:%s%s%s:%s", user, cfg.Host, port, cfg.Path)
		}
	case "rest":
		var cfg struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal([]byte(dest.Config), &cfg); err == nil && cfg.URL != "" {
			return fmt.Sprintf("rest:%s", cfg.URL)
		}
	case "rclone":
		var cfg struct {
			Remote string `json:"remote"`
			Path   string `json:"path"`
		}
		if err := json.Unmarshal([]byte(dest.Config), &cfg); err == nil && cfg.Remote != "" {
			if cfg.Path != "" {
				// rclone addresses a remote as "remote:path"; ensure exactly one
				// colon separates them regardless of whether the user typed it.
				remote := cfg.Remote
				if !strings.HasSuffix(remote, ":") {
					remote += ":"
				}
				return fmt.Sprintf("rclone:%s%s", remote, cfg.Path)
			}
			return fmt.Sprintf("rclone:%s", cfg.Remote)
		}
	}
	return ""
}

// BuildEnv derives backend-specific environment variables from a destination.
// For S3, AWS credentials are extracted from the Credentials JSON.
// For rclone, the credentials JSON is a flat map of RCLONE_CONFIG_* env vars.
func BuildEnv(dest *db.Destination) map[string]string {
	env := make(map[string]string)
	if dest.Credentials == "" {
		return env
	}

	creds := string(dest.Credentials)

	switch dest.Type {
	case "s3":
		var c struct {
			AccessKey string `json:"access_key"`
			SecretKey string `json:"secret_key"`
			Region    string `json:"region"`
		}
		if err := json.Unmarshal([]byte(creds), &c); err == nil {
			if c.AccessKey != "" {
				env["AWS_ACCESS_KEY_ID"] = c.AccessKey
			}
			if c.SecretKey != "" {
				env["AWS_SECRET_ACCESS_KEY"] = c.SecretKey
			}
			if c.Region != "" {
				env["AWS_DEFAULT_REGION"] = c.Region
			}
		}
	case "rclone":
		var c map[string]string
		if err := json.Unmarshal([]byte(creds), &c); err == nil {
			for k, v := range c {
				env[k] = v
			}
		}
	case "rest":
		var c struct {
			User     string `json:"user"`
			Password string `json:"password"`
		}

		if err := json.Unmarshal([]byte(creds), &c); err == nil {
			if c.User != "" {
				env["RESTIC_REST_USERNAME"] = c.User
			}
			if c.Password != "" {
				env["RESTIC_REST_PASSWORD"] = c.Password
			}
		}
	}

	return env
}