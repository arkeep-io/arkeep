package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/api"
	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/db"
	grpcserver "github.com/arkeep-io/arkeep/server/internal/grpc"
	"github.com/arkeep-io/arkeep/server/internal/repository"
	"github.com/arkeep-io/arkeep/server/internal/scheduler"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

type config struct {
	httpAddr   string
	grpcAddr   string
	dbDriver   string
	dbDSN      string
	secretKey  string
	logLevel   string
	dataDir    string
	agentToken string
	secureCookies bool
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

	root.PersistentFlags().StringVar(&cfg.httpAddr, "http-addr", envOrDefault("ARKEEP_HTTP_ADDR", ":8080"), "HTTP API and GUI listen address")
	root.PersistentFlags().StringVar(&cfg.grpcAddr, "grpc-addr", envOrDefault("ARKEEP_GRPC_ADDR", ":9090"), "gRPC server listen address for agents")
	root.PersistentFlags().StringVar(&cfg.dbDriver, "db-driver", envOrDefault("ARKEEP_DB_DRIVER", "sqlite"), "Database driver (sqlite or postgres)")
	root.PersistentFlags().StringVar(&cfg.dbDSN, "db-dsn", envOrDefault("ARKEEP_DB_DSN", "./arkeep.db"), "Database DSN or file path for SQLite")
	root.PersistentFlags().StringVar(&cfg.secretKey, "secret-key", envOrDefault("ARKEEP_SECRET_KEY", ""), "Master secret key for encrypting credentials at rest (required)")
	root.PersistentFlags().StringVar(&cfg.logLevel, "log-level", envOrDefault("ARKEEP_LOG_LEVEL", "info"), "Log level (debug, info, warn, error)")
	root.PersistentFlags().StringVar(&cfg.dataDir, "data-dir", envOrDefault("ARKEEP_DATA_DIR", "./data"), "Directory for server data (RSA keys, etc.)")
	root.PersistentFlags().StringVar(&cfg.agentToken, "agent-token", envOrDefault("ARKEEP_AGENT_TOKEN", ""), "Shared secret for gRPC agent authentication (empty = disabled, dev only)")
	root.PersistentFlags().BoolVar(&cfg.secureCookies, "secure-cookies", envOrDefault("ARKEEP_SECURE_COOKIES", "false") == "true", "Set Secure flag on auth cookies (enable in production over HTTPS)")

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

	// --- Signal handling ---
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// --- 1. Encryption ---
	// InitEncryption must be called before opening the database so that
	// EncryptedString fields can encrypt/decrypt transparently on read/write.
	// The secret key is padded or truncated to exactly 32 bytes (AES-256).
	keyBytes := make([]byte, 32)
	copy(keyBytes, []byte(cfg.secretKey))
	if err := db.InitEncryption(keyBytes); err != nil {
		return fmt.Errorf("failed to initialize encryption: %w", err)
	}

	// --- 2. Database ---
	gormDB, err := db.New(db.Config{
		Driver:   cfg.dbDriver,
		DSN:      cfg.dbDSN,
		Logger:   logger,
		LogLevel: gormLogLevel(cfg.logLevel),
	})
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	sqlDB, err := gormDB.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql.DB: %w", err)
	}
	defer sqlDB.Close()

	// --- 3. Repositories ---
	userRepo := repository.NewUserRepository(gormDB)
	refreshTokenRepo := repository.NewRefreshTokenRepository(gormDB)
	agentRepo := repository.NewAgentRepository(gormDB)
	destinationRepo := repository.NewDestinationRepository(gormDB)
	policyRepo := repository.NewPolicyRepository(gormDB)
	jobRepo := repository.NewJobRepository(gormDB)
	snapshotRepo := repository.NewSnapshotRepository(gormDB)
	notificationRepo := repository.NewNotificationRepository(gormDB)
	oidcProviderRepo := repository.NewOIDCProviderRepository(gormDB)

	// --- 4. Auth ---
	// In development (no data dir or missing key files), ephemeral keys are
	// generated in memory. In production, persistent PEM files are used so
	// tokens survive server restarts.
	jwtManager, err := buildJWTManager(cfg.dataDir, logger)
	if err != nil {
		return fmt.Errorf("failed to initialize JWT manager: %w", err)
	}

	localProvider := auth.NewLocalAuthProvider(userRepo, refreshTokenRepo, jwtManager)
	oidcProvider := auth.NewOIDCAuthProvider(oidcProviderRepo, userRepo, refreshTokenRepo, jwtManager)
	authService := auth.NewAuthService(localProvider, oidcProvider, refreshTokenRepo, jwtManager)

	// --- 5. Agent Manager ---
	agentMgr := agentmanager.New(logger)

	// --- 6. Scheduler ---
	sched, err := scheduler.New(policyRepo, jobRepo, agentMgr, logger)
	if err != nil {
		return fmt.Errorf("failed to create scheduler: %w", err)
	}
	if err := sched.Start(ctx); err != nil {
		return fmt.Errorf("failed to start scheduler: %w", err)
	}
	defer func() {
		if err := sched.Stop(); err != nil {
			logger.Warn("scheduler shutdown error", zap.Error(err))
		}
	}()

	// --- 7. gRPC server ---
	grpcSrv := grpcserver.New(
		grpcserver.Config{
			ListenAddr: cfg.grpcAddr,
			AgentToken: cfg.agentToken,
		},
		agentMgr,
		agentRepo,
		logger,
	)

	go func() {
		if err := grpcSrv.ListenAndServe(ctx, cfg.grpcAddr); err != nil {
			logger.Error("gRPC server error", zap.Error(err))
			cancel()
		}
	}()

	// --- 8. HTTP server ---
	router := api.NewRouter(api.RouterConfig{
		AuthService:   authService,
		Scheduler:     sched,
		Logger:        logger,
		Users:         userRepo,
		Agents:        agentRepo,
		Destinations:  destinationRepo,
		Policies:      policyRepo,
		Jobs:          jobRepo,
		Snapshots:     snapshotRepo,
		Notifications: notificationRepo,
		OIDCProviders: oidcProviderRepo,
		Secure:        cfg.secureCookies,
	})

	httpSrv := &http.Server{
		Addr:         cfg.httpAddr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		logger.Info("http server listening", zap.String("addr", cfg.httpAddr))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", zap.Error(err))
			cancel()
		}
	}()

	// --- Wait for shutdown signal ---
	<-ctx.Done()
	logger.Info("shutting down arkeep server")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http server graceful shutdown error", zap.Error(err))
	}

	logger.Info("arkeep server stopped")
	return nil
}

// buildJWTManager loads RSA keys from the data directory if available,
// or generates ephemeral in-memory keys for development.
func buildJWTManager(dataDir string, logger *zap.Logger) (*auth.JWTManager, error) {
	privPath := filepath.Join(dataDir, "jwt_private.pem")
	pubPath := filepath.Join(dataDir, "jwt_public.pem")

	if _, err := os.Stat(privPath); err == nil {
		logger.Info("loading JWT keys from disk", zap.String("private", privPath))
		return auth.NewJWTManagerFromFiles(privPath, pubPath, "arkeep-server")
	}

	logger.Warn("JWT key files not found — using ephemeral in-memory keys (tokens will be invalidated on restart)",
		zap.String("expected_private", privPath),
	)
	return auth.NewJWTManagerGenerated("arkeep-server")
}

// gormLogLevel maps the application log level string to a GORM logger level.
func gormLogLevel(level string) gormlogger.LogLevel {
	switch level {
	case "debug":
		return gormlogger.Info
	case "info":
		return gormlogger.Warn
	default:
		return gormlogger.Error
	}
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