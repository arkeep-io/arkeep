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
	httpAddr  string
	grpcAddr  string
	dbDriver  string
	dbDSN     string
	secretKey string
	logLevel  string
	dataDir   string
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
		Use:   "arkeep-server",
		Short: "Arkeep server — central backup management server",
		Long: `Arkeep server is the central component of the Arkeep backup system.
It exposes a REST API for the web GUI, a gRPC server for agents,
and manages scheduling, policies, and notifications.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), cfg)
		},
	}

	root.AddCommand(newVersionCmd())
	root.AddCommand(newMigrateCmd(cfg))

	root.PersistentFlags().StringVar(&cfg.httpAddr, "http-addr", envOrDefault("ARKEEP_HTTP_ADDR", ":8080"), "HTTP API and GUI listen address")
	root.PersistentFlags().StringVar(&cfg.grpcAddr, "grpc-addr", envOrDefault("ARKEEP_GRPC_ADDR", ":9090"), "gRPC server listen address for agents")
	root.PersistentFlags().StringVar(&cfg.dbDriver, "db-driver", envOrDefault("ARKEEP_DB_DRIVER", "sqlite"), "Database driver (sqlite or postgres)")
	root.PersistentFlags().StringVar(&cfg.dbDSN, "db-dsn", envOrDefault("ARKEEP_DB_DSN", "./arkeep.db"), "Database DSN or file path for SQLite")
	root.PersistentFlags().StringVar(&cfg.secretKey, "secret-key", envOrDefault("ARKEEP_SECRET_KEY", ""), "Master secret key for encrypting credentials at rest (required)")
	root.PersistentFlags().StringVar(&cfg.logLevel, "log-level", envOrDefault("ARKEEP_LOG_LEVEL", "info"), "Log level (debug, info, warn, error)")
	root.PersistentFlags().StringVar(&cfg.dataDir, "data-dir", envOrDefault("ARKEEP_DATA_DIR", "./data"), "Directory for server data (RSA keys, etc.)")

	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("arkeep-server %s (commit: %s, built: %s)\n", version, commit, date)
		},
	}
}

func newMigrateCmd(cfg *config) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Run database migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: implement migration runner
			return fmt.Errorf("not implemented yet")
		},
	}
}

func run(ctx context.Context, cfg *config) error {
	logger, err := buildLogger(cfg.logLevel)
	if err != nil {
		return fmt.Errorf("failed to build logger: %w", err)
	}
	defer logger.Sync() //nolint:errcheck

	if cfg.secretKey == "" {
		return fmt.Errorf("secret key is required — set --secret-key or ARKEEP_SECRET_KEY")
	}

	logger.Info("starting arkeep server",
		zap.String("version", version),
		zap.String("http_addr", cfg.httpAddr),
		zap.String("grpc_addr", cfg.grpcAddr),
		zap.String("db_driver", cfg.dbDriver),
		zap.String("log_level", cfg.logLevel),
	)

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// TODO: initialize components in order:
	// 1. db.Connect          — database connection and migrations
	// 2. auth.Service        — JWT keys, auth providers
	// 3. agentmanager.Manager — in-memory agent registry
	// 4. scheduler.Scheduler — gocron job scheduler
	// 5. notification.Service — notification dispatcher
	// 6. websocket.Hub       — WebSocket pub/sub hub
	// 7. grpc.Server         — gRPC server for agents (port 9090)
	// 8. api.Router          — HTTP REST API + GUI (port 8080)

	<-ctx.Done()
	logger.Info("shutting down arkeep server")
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