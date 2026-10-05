package main

import (
	"os"
	"path/filepath"
	"strings"
)

// maxHostnameLen is the DNS limit for a fully qualified name; anything longer
// read from a host file is treated as garbage rather than a hostname.
const maxHostnameLen = 253

// resolveHostname returns the hostname the agent reports to the server and
// stamps on restic snapshots, together with where it came from (for logging).
//
// Inside Docker os.Hostname() returns the container's short ID, which is
// meaningless to the user and changes every time the container is recreated
// (issue #284). Resolution order:
//
//  1. override — the --hostname flag / ARKEEP_AGENT_HOSTNAME, when set.
//  2. The host's own hostname file under hostRoot (the host filesystem mount,
//     /hostfs by default inside Docker): etc/hostname, then etc/HOSTNAME
//     (Slackware/Unraid). Skipped when hostRoot is empty (native install).
//  3. os.Hostname(), or "unknown" if even that fails.
func resolveHostname(override, hostRoot string) (hostname, source string) {
	if v := strings.TrimSpace(override); v != "" {
		return v, "override"
	}
	if hostRoot != "" {
		for _, name := range []string{"hostname", "HOSTNAME"} {
			path := filepath.Join(hostRoot, "etc", name)
			if v, ok := readHostnameFile(path); ok {
				return v, path
			}
		}
	}
	if v, err := os.Hostname(); err == nil && v != "" {
		return v, "os"
	}
	return "unknown", "default"
}

// readHostnameFile returns the first line of path when it holds a plausible
// hostname: non-empty, no inner whitespace, at most maxHostnameLen bytes.
func readHostnameFile(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	line, _, _ := strings.Cut(string(data), "\n")
	line = strings.TrimSpace(line)
	if line == "" || len(line) > maxHostnameLen || strings.ContainsAny(line, " \t\r") {
		return "", false
	}
	return line, true
}
