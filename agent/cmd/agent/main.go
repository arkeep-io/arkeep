// Package main is the entry point for the arkeep-agent binary.
// It wires all internal packages together and starts the connection loop.
//
// Startup sequence:
//  1. Parse CLI flags / environment variables
//  2. Build logger
//  3. Extract embedded restic and rclone binaries (idempotent)
//  4. Optionally connect to Docker (non-fatal if unavailable)
//  5. Build executor (job queue + restic wrapper + hooks runner)
//  6. Build connection manager (gRPC client)
//  7. Start executor worker and connection loop
//  8. Block until SIGINT/SIGTERM, then graceful shutdown
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/agent/internal/connection"
	"github.com/arkeep-io/arkeep/agent/internal/docker"
	"github.com/arkeep-io/arkeep/agent/internal/executor"
	"github.com/arkeep-io/arkeep/agent/internal/hooks"
	"github.com/arkeep-io/arkeep/agent/internal/restic"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

type config struct {
	serverAddr   string
	sharedSecret string
	stateDir     string
	dockerSocket string
	logLevel     string
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
		Short: "Arkeep agent — backup agent for the Arkeep system",
		Long: `Arkeep agent runs on each machine to be backed up.
It connects to the Arkeep server via a persistent gRPC stream,
receives backup jobs, and executes them using the embedded restic binary.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), cfg)
		},
	}

	root.AddCommand(newVersionCmd())

	root.PersistentFlags().StringVar(&cfg.serverAddr, "server-addr", envOrDefault("ARKEEP_SERVER", "localhost:9090"), "Arkeep server gRPC address (host:port)")
	root.PersistentFlags().StringVar(&cfg.sharedSecret, "agent-secret", envOrDefault("ARKEEP_AGENT_SECRET", ""), "Shared secret for gRPC authentication (must match server ARKEEP_AGENT_SECRET)")
	root.PersistentFlags().StringVar(&cfg.stateDir, "state-dir", envOrDefault("ARKEEP_STATE_DIR", defaultStateDir()), "Directory for agent state (agent-state.json, extracted binaries)")
	root.PersistentFlags().StringVar(&cfg.dockerSocket, "docker-socket", envOrDefault("ARKEEP_DOCKER_SOCKET", ""), "Docker socket path (empty = platform default)")
	root.PersistentFlags().StringVar(&cfg.logLevel, "log-level", envOrDefault("ARKEEP_LOG_LEVEL", "info"), "Log level (debug, info, warn, error)")

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

func run(ctx context.Context, cfg *config) error {
	logger, err := buildLogger(cfg.logLevel)
	if err != nil {
		return fmt.Errorf("failed to build logger: %w", err)
	}
	defer logger.Sync() //nolint:errcheck

	if cfg.sharedSecret == "" {
		logger.Warn("agent-secret not configured — gRPC connection is unauthenticated (set ARKEEP_AGENT_SECRET in production)")
	}

	logger.Info("starting arkeep agent",
		zap.String("version", version),
		zap.String("server", cfg.serverAddr),
		zap.String("state_dir", cfg.stateDir),
	)

	// --- Signal handling ---
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// --- Extract embedded binaries ---
	// Idempotent: skips extraction if the file already exists with the
	// correct size. Must run before NewWrapper.
	extractor := restic.NewExtractor(cfg.stateDir)
	wrapper, err := restic.NewWrapper(extractor)
	if err != nil {
		return fmt.Errorf("failed to prepare restic/rclone binaries: %w", err)
	}
	logger.Info("restic and rclone binaries ready")

	// --- Docker client (optional) ---
	// Docker is best-effort: if the socket is unavailable or the daemon is
	// not running, the agent starts normally but rejects jobs that require
	// Docker volume discovery. The connection manager advertises Docker
	// capability in the Register RPC only when the ping succeeds.
	var dockerClient *docker.Client
	dockerAvailable := false

	dc, err := docker.NewClient(cfg.dockerSocket)
	if err != nil {
		logger.Warn("failed to create Docker client, Docker volume backup unavailable",
			zap.Error(err),
		)
	} else {
		if pingErr := dc.Ping(ctx); pingErr != nil {
			logger.Warn("Docker daemon unreachable, Docker volume backup unavailable",
				zap.Error(pingErr),
			)
			dc.Close()
		} else {
			dockerClient = dc
			dockerAvailable = true
			defer dockerClient.Close()
			logger.Info("Docker daemon reachable, Docker volume backup available")
		}
	}

	// --- Hooks runner ---
	hooksRunner := hooks.NewRunner(0) // 0 = use DefaultTimeout (5 minutes)

	// --- Executor ---
	exec := executor.New(wrapper, dockerClient, hooksRunner, logger)

	// --- Connection manager ---
	connCfg := connection.Config{
		ServerAddr:      cfg.serverAddr,
		SharedSecret:    cfg.sharedSecret,
		StateDir:        cfg.stateDir,
		Version:         version,
		DockerAvailable: dockerAvailable,
	}

	// Pass Docker availability so the connection manager can advertise it
	// in the AgentCapabilities sent during Register.

	mgr := connection.New(connCfg, exec, logger)

	// --- Start ---
	// The executor worker and connection manager run concurrently.
	// Both respect ctx cancellation for graceful shutdown.
	go exec.Run(ctx, mgr, mgr)

	// Run blocks until ctx is cancelled (SIGINT/SIGTERM).
	mgr.Run(ctx)

	logger.Info("arkeep agent stopped")
	return nil
}

// defaultStateDir returns the platform-appropriate default state directory.
// On Linux/macOS: ~/.arkeep
// On Windows:     %APPDATA%\arkeep
func defaultStateDir() string {
	if dir, err := os.UserHomeDir(); err == nil {
		return dir + "/.arkeep"
	}
	return ".arkeep"
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