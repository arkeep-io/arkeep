package main

import (
	"strings"
	"testing"
)

// TestSecretsStayOutOfHelp: the help, which cobra also prints when the server
// stops on an error, must not show the secrets read from the environment.
// v0.12.0 printed them as the flags' default values in the container logs.
func TestSecretsStayOutOfHelp(t *testing.T) {
	secrets := map[string]string{
		"ARKEEP_SECRET_KEY":    "secret-key-from-env",
		"ARKEEP_AGENT_SECRET":  "agent-secret-from-env",
		"ARKEEP_METRICS_TOKEN": "metrics-token-from-env",
	}
	for k, v := range secrets {
		t.Setenv(k, v)
	}

	root := newRootCmd()
	if !root.SilenceUsage {
		t.Error("SilenceUsage is off: a runtime error prints the help")
	}
	help := root.UsageString()
	for k, v := range secrets {
		if strings.Contains(help, v) {
			t.Errorf("the help shows the value of %s", k)
		}
	}

	cfg := &config{}
	cfg.secretsFromEnv()
	if cfg.secretKey != secrets["ARKEEP_SECRET_KEY"] || cfg.agentSecret != secrets["ARKEEP_AGENT_SECRET"] || cfg.metricsToken != secrets["ARKEEP_METRICS_TOKEN"] {
		t.Errorf("secrets not read from the environment: %+v", cfg)
	}

	cfg = &config{secretKey: "from-flag"}
	cfg.secretsFromEnv()
	if cfg.secretKey != "from-flag" {
		t.Errorf("the environment overrides --secret-key: got %q", cfg.secretKey)
	}
}
