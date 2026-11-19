package main

import (
	"strings"
	"testing"
)

// TestAgentSecretStaysOutOfHelp: the help, which cobra also prints when the
// agent stops on an error, must not show ARKEEP_AGENT_SECRET.
func TestAgentSecretStaysOutOfHelp(t *testing.T) {
	t.Setenv("ARKEEP_AGENT_SECRET", "agent-secret-from-env")

	root := newRootCmd()
	if !root.SilenceUsage {
		t.Error("SilenceUsage is off: a runtime error prints the help")
	}
	if strings.Contains(root.UsageString(), "agent-secret-from-env") {
		t.Error("the help shows the value of ARKEEP_AGENT_SECRET")
	}

	cfg := &config{}
	cfg.secretsFromEnv()
	if cfg.sharedSecret != "agent-secret-from-env" {
		t.Errorf("agent secret not read from the environment: %q", cfg.sharedSecret)
	}
	cfg = &config{sharedSecret: "from-flag"}
	cfg.secretsFromEnv()
	if cfg.sharedSecret != "from-flag" {
		t.Errorf("the environment overrides --agent-secret: got %q", cfg.sharedSecret)
	}
}
