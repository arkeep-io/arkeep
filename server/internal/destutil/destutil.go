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

// sftpRcloneRemote is the fixed name of the synthetic rclone remote used to
// route SFTP destinations through rclone. It must match the RCLONE_CONFIG_<NAME>_*
// env var prefix built in BuildEnv (uppercased: RCLONE_CONFIG_ARKEEPSFTP_*).
const sftpRcloneRemote = "arkeepsftp"

// sftpConfig is the non-secret part of an SFTP destination, stored in the
// destination Config JSON. Port is a string because the GUI stores it as one.
type sftpConfig struct {
	Host string `json:"host"`
	User string `json:"user"`
	Path string `json:"path"`
	Port string `json:"port"`
}

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
		// SFTP is routed through the embedded rclone binary rather than restic's
		// native sftp backend: the native backend shells out to the system `ssh`
		// client (keys in ~/.ssh, ssh-agent), which does not exist in the minimal
		// agent container. rclone's sftp backend is pure Go and accepts the key
		// and password inline via the synthetic remote configured in BuildEnv.
		var cfg sftpConfig
		if err := json.Unmarshal([]byte(dest.Config), &cfg); err == nil && cfg.Host != "" {
			return fmt.Sprintf("rclone:%s:%s", sftpRcloneRemote, cfg.Path)
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
		}
		if err := json.Unmarshal([]byte(dest.Config), &cfg); err == nil && cfg.Remote != "" {
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
	// SFTP derives its connection env from Config (host/user/port), which is
	// independent of Credentials, so it must run even when no credentials are set.
	if dest.Type == "sftp" {
		buildSFTPEnv(dest, env)
		return env
	}
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

// buildSFTPEnv populates env with the RCLONE_CONFIG_<REMOTE>_* variables that
// define the synthetic rclone remote used for SFTP. Host/user/port come from
// the Config JSON; the private key and password come from the (decrypted)
// Credentials JSON. The private key is passed inline via key_pem (rclone wants
// it on a single line with newlines as the literal "\n"); the password is
// obscured as rclone requires. Host key verification is intentionally left at
// rclone's default (none).
func buildSFTPEnv(dest *db.Destination, env map[string]string) {
	var cfg sftpConfig
	if err := json.Unmarshal([]byte(dest.Config), &cfg); err != nil || cfg.Host == "" {
		return
	}

	prefix := "RCLONE_CONFIG_" + strings.ToUpper(sftpRcloneRemote) + "_"
	env[prefix+"TYPE"] = "sftp"
	env[prefix+"HOST"] = cfg.Host
	if cfg.User != "" {
		env[prefix+"USER"] = cfg.User
	}
	if cfg.Port != "" && cfg.Port != "22" {
		env[prefix+"PORT"] = cfg.Port
	}

	if dest.Credentials == "" {
		return
	}
	var creds struct {
		Password   string `json:"password"`
		PrivateKey string `json:"private_key"`
	}
	if err := json.Unmarshal([]byte(dest.Credentials), &creds); err != nil {
		return
	}
	if creds.PrivateKey != "" {
		// rclone key_pem expects the PEM on a single line with newlines as "\n".
		normalized := strings.ReplaceAll(creds.PrivateKey, "\r\n", "\n")
		env[prefix+"KEY_PEM"] = strings.ReplaceAll(normalized, "\n", "\\n")
	}
	if creds.Password != "" {
		if obscured, err := obscure(creds.Password); err == nil {
			env[prefix+"PASS"] = obscured
		}
	}
}