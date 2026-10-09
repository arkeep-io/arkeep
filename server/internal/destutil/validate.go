package destutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ErrRcloneCredentials rejects credentials on an rclone destination. rclone
// remotes are configured in rclone.conf on the agent, so there is nothing to
// store, and the server never turns credentials into agent env vars.
var ErrRcloneCredentials = errors.New("rclone destinations take no credentials: configure the remote in rclone.conf on the agent")

// ValidateConfig checks the Config JSON of a destination of the given type:
// the fields BuildRepoURL and BuildEnv need must be present and well formed.
// It mirrors the per-type schemas of the GUI (DestinationSheet.vue), so a
// request that bypasses the GUI cannot store a destination that can never run.
func ValidateConfig(destType, config string) error {
	cfg, err := decodeStringMap(config, "config")
	if err != nil {
		return err
	}
	switch destType {
	case "local":
		return requireFields(cfg, "config", "path")
	case "s3":
		return requireFields(cfg, "config", "bucket", "endpoint")
	case "sftp":
		if err := requireFields(cfg, "config", "host", "user", "path"); err != nil {
			return err
		}
		if port := strings.TrimSpace(cfg["port"]); port != "" {
			if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
				return errors.New("config.port must be a number between 1 and 65535")
			}
		}
	case "rest":
		if err := requireFields(cfg, "config", "url"); err != nil {
			return err
		}
		u, err := url.Parse(cfg["url"])
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("config.url must be an http or https URL")
		}
	case "rclone":
		if err := requireFields(cfg, "config", "remote", "path"); err != nil {
			return err
		}
		return ValidateRcloneRemote(cfg["remote"])
	default:
		return fmt.Errorf("unknown destination type %q", destType)
	}
	return nil
}

// ValidateCredentials checks the Credentials JSON of a destination of the
// given type. It applies to a full set of credentials: callers that treat
// blank credentials as "keep the stored ones" must skip it in that case.
func ValidateCredentials(destType, creds string) error {
	if strings.TrimSpace(creds) == "" {
		creds = "{}"
	}
	c, err := decodeStringMap(creds, "credentials")
	if err != nil {
		return err
	}
	switch destType {
	case "s3":
		return requireFields(c, "credentials", "access_key", "secret_key")
	case "rclone":
		for _, v := range c {
			if strings.TrimSpace(v) != "" {
				return ErrRcloneCredentials
			}
		}
	}
	return nil
}

// decodeStringMap parses a JSON object whose values are all strings, which is
// the shape the GUI sends and the one BuildRepoURL and BuildEnv read.
func decodeStringMap(raw, field string) (map[string]string, error) {
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
		return nil, fmt.Errorf("%s must be a JSON object of strings", field)
	}
	return m, nil
}

// requireFields reports the first of names that is missing or blank in m.
func requireFields(m map[string]string, field string, names ...string) error {
	for _, n := range names {
		if strings.TrimSpace(m[n]) == "" {
			return fmt.Errorf("%s.%s is required", field, n)
		}
	}
	return nil
}
