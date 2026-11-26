// Package childenv builds the environment of the processes the agent starts:
// restic, and through hooks and command sources, the user's own commands.
//
// The agent is configured through ARKEEP_* variables, and one of them,
// ARKEEP_AGENT_SECRET, is the secret every agent of an installation shares to
// enroll. None of them concerns a child process, and a hook that prints its
// environment would copy them into the job log, which every signed-in user
// can read. They are therefore removed from every child's environment.
package childenv

import "strings"

// agentPrefix starts the name of every variable that configures the agent.
const agentPrefix = "ARKEEP_"

// Filter returns environ, a list of "name=value" entries as returned by
// os.Environ, without the agent's own variables. Names are compared
// case-insensitively because Windows env names are case-insensitive.
// environ itself is not modified.
func Filter(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		if IsAgentVar(name(kv)) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// IsAgentVar reports whether name is one of the agent's own variables.
func IsAgentVar(name string) bool {
	return len(name) >= len(agentPrefix) && strings.EqualFold(name[:len(agentPrefix)], agentPrefix)
}

// name returns the variable name of a "name=value" entry. Windows keeps
// per-drive working directories in entries such as "=C:=C:\dir", whose name
// starts with "=": the separator is searched from the second character.
func name(kv string) string {
	if i := strings.IndexByte(kv[min(1, len(kv)):], '='); i >= 0 {
		return kv[:i+min(1, len(kv))]
	}
	return kv
}
