package destutil

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SFTPKnownHostsEnv carries the known_hosts content of an SFTP destination to
// the agent. It is not an rclone variable: the agent writes the content to a
// private file and points rclone's known_hosts_file option at it, so the server
// never chooses a path on the agent (SEC-31).
const SFTPKnownHostsEnv = "ARKEEP_SFTP_KNOWN_HOSTS"

// ParseHostKeys parses the host key field of an SFTP destination: one or more
// lines as printed by ssh-keyscan ("host type base64", hashed hosts included)
// or bare public keys ("type base64"). Blank lines and # comments are skipped.
// The host part of a line is ignored: the destination's own host and port are
// what the agent checks.
func ParseHostKeys(text string) ([]ssh.PublicKey, error) {
	var keys []ssh.PublicKey
	for i, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "@") {
			return nil, fmt.Errorf("line %d: @cert-authority and @revoked markers are not supported", i+1)
		}
		if _, _, key, _, _, err := ssh.ParseKnownHosts([]byte(line)); err == nil {
			keys = append(keys, key)
			continue
		}
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("line %d is not an SSH host key", i+1)
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, errors.New("no host key found: paste the output of ssh-keyscan")
	}
	return keys, nil
}

// knownHostsContent builds a known_hosts file trusting keys for host:port.
func knownHostsContent(host, port string, keys []ssh.PublicKey) string {
	if port == "" {
		port = "22"
	}
	addr := []string{net.JoinHostPort(host, port)}
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(knownhosts.Line(addr, k))
		sb.WriteString("\n")
	}
	return sb.String()
}
