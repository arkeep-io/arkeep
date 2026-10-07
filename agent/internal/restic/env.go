package restic

import "strings"

// sftpRcloneEnvPrefix is the env prefix of the synthetic rclone remote the
// server builds for SFTP destinations (server/internal/destutil, remote name
// "arkeepsftp").
const sftpRcloneEnvPrefix = "RCLONE_CONFIG_ARKEEPSFTP_"

// allowedDestEnv lists every backend env var the server may legitimately send
// in Destination.Env. Anything else is dropped before restic runs: restic and
// rclone honour variables such as RESTIC_PASSWORD_COMMAND, RESTIC_REPOSITORY
// or LD_PRELOAD that would let whoever configured a destination run commands
// on this host or redirect the repository. A new destination type that needs
// env vars must add them both here and in destutil.BuildEnv on the server.
var allowedDestEnv = map[string]bool{
	// S3
	"AWS_ACCESS_KEY_ID":     true,
	"AWS_SECRET_ACCESS_KEY": true,
	"AWS_DEFAULT_REGION":    true,
	// REST server
	"RESTIC_REST_USERNAME": true,
	"RESTIC_REST_PASSWORD": true,
	// SFTP (through the synthetic rclone remote)
	sftpRcloneEnvPrefix + "TYPE":    true,
	sftpRcloneEnvPrefix + "HOST":    true,
	sftpRcloneEnvPrefix + "USER":    true,
	sftpRcloneEnvPrefix + "PORT":    true,
	sftpRcloneEnvPrefix + "KEY_PEM": true,
	sftpRcloneEnvPrefix + "PASS":    true,
}

// filterDestEnv splits env into the allowed variables and the names of the
// rejected ones. Keys are compared case-insensitively because Windows env
// names are case-insensitive.
func filterDestEnv(env map[string]string) (allowed map[string]string, rejected []string) {
	allowed = make(map[string]string, len(env))
	for k, v := range env {
		if allowedDestEnv[strings.ToUpper(k)] {
			allowed[k] = v
		} else {
			rejected = append(rejected, k)
		}
	}
	return allowed, rejected
}
