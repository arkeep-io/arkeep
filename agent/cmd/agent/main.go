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
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

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
	serverAddr     string
	serverHTTPAddr string
	sharedSecret   string
	stateDir       string
	dockerSocket   string
	logLevel       string
	grpcTLSCA      string
	grpcInsecure   bool
	dockerHostRoot string
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

	root.PersistentFlags().StringVar(&cfg.serverAddr, "server-addr", envOrDefault("ARKEEP_SERVER_ADDR", "localhost:9090"), "Arkeep server gRPC address (host:port)")
	root.PersistentFlags().StringVar(&cfg.sharedSecret, "agent-secret", envOrDefault("ARKEEP_AGENT_SECRET", ""), "Shared secret for gRPC authentication (must match server ARKEEP_AGENT_SECRET)")
	root.PersistentFlags().StringVar(&cfg.stateDir, "state-dir", envOrDefault("ARKEEP_STATE_DIR", defaultStateDir()), "Directory for agent state (agent-state.json, extracted binaries)")
	root.PersistentFlags().StringVar(&cfg.dockerSocket, "docker-socket", envOrDefault("ARKEEP_DOCKER_SOCKET", ""), "Docker socket path (empty = platform default)")
	root.PersistentFlags().StringVar(&cfg.logLevel, "log-level", envOrDefault("ARKEEP_LOG_LEVEL", "info"), "Log level (debug, info, warn, error)")
	root.PersistentFlags().StringVar(&cfg.grpcTLSCA, "grpc-tls-ca", envOrDefault("ARKEEP_GRPC_TLS_CA", ""), "Path to CA certificate for gRPC TLS (for self-signed server certs; leave empty for system pool)")
	root.PersistentFlags().BoolVar(&cfg.grpcInsecure, "grpc-insecure", envOrDefault("ARKEEP_GRPC_INSECURE", "false") == "true", "Disable TLS for gRPC transport (development only — never use in production)")
	root.PersistentFlags().StringVar(&cfg.serverHTTPAddr, "server-http-addr", envOrDefault("ARKEEP_SERVER_HTTP_ADDR", ""), "Base URL of the server HTTP API for enrollment (default: derived from --server-addr with port 8080)")
	root.PersistentFlags().StringVar(&cfg.dockerHostRoot, "docker-host-root", envOrDefault("ARKEEP_DOCKER_HOST_ROOT", ""), "Container path where the host filesystem is mounted (default: /hostfs when running inside Docker, empty otherwise). Override only if you mount the host filesystem at a custom path.")

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

	// Auto-detect Docker: default docker-host-root to /hostfs when not set explicitly.
	if cfg.dockerHostRoot == "" && isRunningInDocker() {
		cfg.dockerHostRoot = "/hostfs"
		logger.Info("running inside Docker: defaulting docker-host-root to /hostfs " +
			"(override with --docker-host-root or ARKEEP_DOCKER_HOST_ROOT)")
	}

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
			if err := dc.Close(); err != nil {
				logger.Warn("failed to close Docker client", zap.Error(err))
			}
		} else {
			dockerClient = dc
			dockerAvailable = true
			defer func() {
				if err := dockerClient.Close(); err != nil {
					logger.Warn("failed to close Docker client", zap.Error(err))
				}
			}()
			logger.Info("Docker daemon reachable, Docker volume backup available")
		}
	}

	// --- Hooks runner ---
	hooksRunner := hooks.NewRunner(0) // 0 = use DefaultTimeout (5 minutes)

	// --- Executor ---
	exec := executor.New(wrapper, dockerClient, hooksRunner, logger, cfg.dockerHostRoot)

	// --- Load mTLS credentials from state-dir (written by enrollment) ---
	// If all three files are present the agent was enrolled previously and can
	// skip the enrollment step. cfg.grpcTLSCA is only overwritten when it was
	// not set explicitly on the command line, so a user-supplied CA always wins.
	clientCertFile := filepath.Join(cfg.stateDir, "grpc-client.crt")
	clientKeyFile := filepath.Join(cfg.stateDir, "grpc-client.key")
	stateCAFile := filepath.Join(cfg.stateDir, "grpc-ca.crt")
	if fileExists(clientCertFile) && fileExists(clientKeyFile) && fileExists(stateCAFile) {
		if cfg.grpcTLSCA == "" {
			cfg.grpcTLSCA = stateCAFile
		}
	} else {
		// Reset so the connection manager knows enrollment is needed.
		clientCertFile = ""
		clientKeyFile = ""
	}

	// --- Derive server HTTP address if not set explicitly ---
	serverHTTPAddr := cfg.serverHTTPAddr
	if serverHTTPAddr == "" {
		serverHTTPAddr = deriveHTTPAddr(cfg.serverAddr)
	}

	// --- Connection manager ---
	connCfg := connection.Config{
		ServerAddr:      cfg.serverAddr,
		ServerHTTPAddr:  serverHTTPAddr,
		SharedSecret:    cfg.sharedSecret,
		StateDir:        cfg.stateDir,
		Version:         version,
		DockerAvailable: dockerAvailable,
		TLSCAFile:       cfg.grpcTLSCA,
		ClientCertFile:  clientCertFile,
		ClientKeyFile:   clientKeyFile,
		Insecure:        cfg.grpcInsecure,
	}

	// Pass dockerClient so the connection manager can handle JOB_TYPE_LIST_VOLUMES
	// requests from the server. May be nil if Docker is unavailable on this host.
	mgr := connection.New(connCfg, exec, dockerClient, logger)

	// --- Start ---
	// The executor worker and connection manager run concurrently.
	// Both respect ctx cancellation for graceful shutdown.
	//
	// Shutdown sequence:
	//  1. SIGINT/SIGTERM cancels ctx
	//  2. mgr.Run returns (gRPC loops exit via ctx.Done)
	//  3. We wait up to 30 s for the executor to finish its current job
	//     (restic/rclone subprocess is killed by exec.CommandContext when ctx
	//     is cancelled, so this is usually just a few seconds)
	//  4. If the executor doesn't finish in time we log a warning and exit
	//     anyway — the server-side orphan recovery will mark the job failed.
	execDone := make(chan struct{})
	go func() {
		exec.Run(ctx, mgr, mgr)
		close(execDone)
	}()

	// mgr.Run blocks until ctx is cancelled (SIGINT/SIGTERM).
	mgr.Run(ctx)

	logger.Info("shutdown signal received, waiting for executor to finish")

	const shutdownTimeout = 30 * time.Second
	shutdownTimer := time.NewTimer(shutdownTimeout)
	defer shutdownTimer.Stop()

	select {
	case <-execDone:
		logger.Info("arkeep agent stopped cleanly")
	case <-shutdownTimer.C:
		logger.Warn("executor did not finish within shutdown timeout — exiting anyway",
			zap.Duration("timeout", shutdownTimeout),
		)
	}

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

// isRunningInDocker reports whether the process is running inside a Docker
// container. It checks /.dockerenv first (created by the Docker runtime in
// most configurations), then falls back to scanning /proc/self/cgroup for
// "docker" or "containerd" markers — necessary on some platforms (e.g. Unraid)
// where the container root is an overlay mount that may not include /.dockerenv.
func isRunningInDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return false
	}
	return bytes.Contains(data, []byte("docker")) || bytes.Contains(data, []byte("containerd"))
}

// fileExists returns true if path exists and is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// deriveHTTPAddr constructs the HTTP base URL from a gRPC address by replacing
// the port with 8080 and prepending "http://". For example:
//
//	"arkeep.example.com:9090" → "http://arkeep.example.com:8080"
//	"localhost:9090"           → "http://localhost:8080"
func deriveHTTPAddr(grpcAddr string) string {
	host, _, err := net.SplitHostPort(grpcAddr)
	if err != nil {
		// grpcAddr has no port — use it as-is.
		return "http://" + grpcAddr + ":8080"
	}
	return "http://" + net.JoinHostPort(host, "8080")
}