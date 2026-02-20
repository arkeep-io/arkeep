package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

type config struct {
	serverAddr   string
	agentToken   string
	logLevel     string
	dockerSocket string
	dataDir      string
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cfg := &config{}

	root := &cobra.Command{
		Use:   "arkeep-agent",
		Short: "Arkeep agent — connects to the Arkeep server and executes backup jobs",
		Long: `Arkeep agent runs on each machine you want to back up.
It connects to the Arkeep server via a persistent gRPC stream,
executes backup jobs, and streams logs and status back to the server.
The agent never exposes any port — it always initiates the connection.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), cfg)
		},
	}

	root.AddCommand(newVersionCmd())
	root.AddCommand(newRegisterCmd(cfg))

	root.PersistentFlags().StringVar(&cfg.serverAddr, "server", envOrDefault("ARKEEP_SERVER", "localhost:9090"), "Arkeep server gRPC address (host:port)")
	root.PersistentFlags().StringVar(&cfg.agentToken, "token", envOrDefault("ARKEEP_TOKEN", ""), "Agent authentication token")
	root.PersistentFlags().StringVar(&cfg.logLevel, "log-level", envOrDefault("ARKEEP_LOG_LEVEL", "info"), "Log level (debug, info, warn, error)")
	root.PersistentFlags().StringVar(&cfg.dockerSocket, "docker-socket", envOrDefault("ARKEEP_DOCKER_SOCKET", "/var/run/docker.sock"), "Docker socket path")
	root.PersistentFlags().StringVar(&cfg.dataDir, "data-dir", envOrDefault("ARKEEP_DATA_DIR", "./data"), "Directory for agent state and restic cache")

	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("arkeep-agent %s (commit: %s, built: %s)\n", version, commit, date)
		},
	}
}

func newRegisterCmd(cfg *config) *cobra.Command {
	var registrationToken string

	cmd := &cobra.Command{
		Use:   "register",
		Short: "Register this agent with the Arkeep server",
		Long: `Register connects to the Arkeep server using a one-time registration token
generated from the Arkeep GUI, and saves the permanent agent token to the data directory.
Run this once before starting the agent normally.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: implement registration flow in connection package
			return fmt.Errorf("not implemented yet")
		},
	}

	cmd.Flags().StringVar(&registrationToken, "registration-token", "", "One-time registration token from the Arkeep GUI (required)")
	_ = cmd.MarkFlagRequired("registration-token")

	return cmd
}

func run(ctx context.Context, cfg *config) error {
	logger, err := buildLogger(cfg.logLevel)
	if err != nil {
		return fmt.Errorf("failed to build logger: %w", err)
	}
	defer logger.Sync() //nolint:errcheck

	if cfg.agentToken == "" {
		return fmt.Errorf("agent token is required — run 'arkeep-agent register' first or set ARKEEP_TOKEN")
	}

	logger.Info("starting arkeep agent",
		zap.String("version", version),
		zap.String("server", cfg.serverAddr),
		zap.String("log_level", cfg.logLevel),
		zap.String("data_dir", cfg.dataDir),
	)

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// TODO: initialize components in order:
	// 1. docker.Discovery   — Docker socket watcher (optional, skips if socket unavailable)
	// 2. executor.Queue     — job execution queue
	// 3. connection.Manager — gRPC connection to server (starts last, triggers everything)

	<-ctx.Done()
	logger.Info("shutting down arkeep agent")
	return nil
}

func buildLogger(level string) (*zap.Logger, error) {
	var cfg zap.Config

	switch level {
	case "debug":
		cfg = zap.NewDevelopmentConfig()
	default:
		cfg = zap.NewProductionConfig()
	}

	switch level {
	case "debug":
		cfg.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info":
		cfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "warn":
		cfg.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		cfg.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		cfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	return cfg.Build()
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}