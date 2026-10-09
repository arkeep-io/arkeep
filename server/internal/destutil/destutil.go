// Package destutil provides helpers for building restic repository URLs and
// backend environment variables from db.Destination records. These functions
// are shared between the scheduler (backup dispatch) and the API layer
// (restore dispatch) to ensure consistent URL construction.
package destutil

import (
	"encoding/json"
	"fmt"
	"regexp"
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
			// Prefix is the folder inside the bucket, as the GUI saves it.
			Prefix string `json:"prefix"`
			// Path is the legacy name of the same setting, still honoured
			// when no prefix is set.
			Path string `json:"path"`
		}
		err := json.Unmarshal([]byte(dest.Config), &cfg)
		// Normalise the slashes the three parts are joined with: an endpoint
		// saved as "https://host/" would otherwise yield "host//bucket", which
		// restic parses as an empty bucket name.
		bucket := strings.Trim(strings.TrimSpace(cfg.Bucket), "/")
		if err == nil && bucket != "" {
			endpoint := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
			if endpoint == "" {
				// Legacy compatibility only: destinations created before the
				// GUI required Endpoint (DestinationSheet.vue) may still have
				// it blank. New/edited destinations always send an explicit
				// value, so this fallback should only ever fire for
				// untouched pre-existing rows — it is not an intended
				// default for new S3-compatible destinations (e.g. Backblaze
				// B2), which would otherwise silently point at AWS.
				endpoint = "s3.amazonaws.com"
			}
			if prefix := strings.Trim(strings.TrimSpace(cfg.Prefix), "/"); prefix != "" {
				return fmt.Sprintf("s3:%s/%s/%s", endpoint, bucket, prefix)
			}
			path := strings.TrimSpace(cfg.Path)
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			return fmt.Sprintf("s3:%s/%s%s", endpoint, bucket, path)
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
			Path   string `json:"path"`
		}
		// An invalid remote yields no URL (the job then fails to dispatch)
		// rather than handing restic a connection string written by whoever
		// saved the destination; see ValidateRcloneRemote.
		if err := json.Unmarshal([]byte(dest.Config), &cfg); err == nil && cfg.Remote != "" && ValidateRcloneRemote(cfg.Remote) == nil {
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

// rcloneRemoteName matches a plain rclone remote name: letters, digits and
// _ . + @ - and space, not starting with "-" or a space. It deliberately
// excludes ":" and "," so that a value can never be an rclone connection
// string (":sftp,host=…:" or "remote,opt=value:"), whose options can make
// rclone run an external program on the agent.
var rcloneRemoteName = regexp.MustCompile(`^[\p{L}\p{N}_.+@][\p{L}\p{N}_.+@ -]*$`)

// ValidateRcloneRemote checks the "remote" field of an rclone destination.
// The value names a remote that must already be configured in rclone.conf on
// the agent, optionally followed by ":path" (e.g. "myremote:bucket"). Only the
// name before the first colon is constrained; the path is opaque to rclone's
// option parsing.
func ValidateRcloneRemote(remote string) error {
	name, _, _ := strings.Cut(remote, ":")
	if !rcloneRemoteName.MatchString(name) {
		return fmt.Errorf("remote must be the name of a remote configured in rclone.conf on the agent (letters, digits, _ . + @ - and spaces), optionally followed by :path")
	}
	return nil
}

// BuildEnv derives backend-specific environment variables from a destination.
// For S3, AWS credentials are extracted from the Credentials JSON. rclone
// destinations take no credentials: the remote is configured in rclone.conf
// on the agent, and accepting arbitrary env vars here would let whoever saves
// a destination set e.g. RESTIC_PASSWORD_COMMAND or LD_PRELOAD on the agent.
// Every key produced here must also be allowed by the agent's env allowlist
// (agent/internal/restic/env.go).
func BuildEnv(dest *db.Destination) map[string]string {
	env := make(map[string]string)
	// SFTP derives its connection env from Config (host/user/port), which is
	// independent of Credentials, so it must run even when no credentials are set.
	if dest.Type == "sftp" {
		buildSFTPEnv(dest, env)
		return env
	}
	// The GUI stores the S3 region in Config (DestinationSheet.vue); it is
	// not a secret and must be applied even when no credentials are set.
	if dest.Type == "s3" {
		var cfg struct {
			Region string `json:"region"`
		}
		if err := json.Unmarshal([]byte(dest.Config), &cfg); err == nil {
			if region := strings.TrimSpace(cfg.Region); region != "" {
				env["AWS_DEFAULT_REGION"] = region
			}
		}
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
			// Legacy fallback: region in Credentials, honoured only when
			// Config does not set one.
			if _, ok := env["AWS_DEFAULT_REGION"]; !ok && c.Region != "" {
				env["AWS_DEFAULT_REGION"] = c.Region
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

// RepoPassword returns the password that opens the destination's repository:
// the one stored on the destination, otherwise the first one carried by an
// attached policy — every policy writing here shares the same repository.
// Destinations created without importing a repository only have it on their
// policies. Returns "" when none is known.
func RepoPassword(dest *db.Destination, policies []db.Policy) string {
	if dest.RepoPassword != "" {
		return string(dest.RepoPassword)
	}
	for _, p := range policies {
		if p.RepoPassword != "" {
			return string(p.RepoPassword)
		}
	}
	return ""
}
