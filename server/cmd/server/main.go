package main

import (
	"context"
	"crypto/sha256"
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
	"github.com/arkeep-io/arkeep/server/internal/notification"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/scheduler"
	"github.com/arkeep-io/arkeep/server/internal/telemetry"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

type config struct {
	httpAddr      string
	grpcAddr      string
	grpcTLSCert   string
	grpcTLSKey    string
	dbDriver      string
	dbDSN         string
	secretKey     string
	logLevel      string
	dataDir       string
	agentSecret   string
	secureCookies bool
	telemetry     bool
	grpcInsecure  bool
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
	root.PersistentFlags().StringVar(&cfg.grpcTLSCert, "grpc-tls-cert", envOrDefault("ARKEEP_GRPC_TLS_CERT", ""), "Path to PEM certificate file for gRPC TLS (requires --grpc-tls-key)")
	root.PersistentFlags().StringVar(&cfg.grpcTLSKey, "grpc-tls-key", envOrDefault("ARKEEP_GRPC_TLS_KEY", ""), "Path to PEM private key file for gRPC TLS (requires --grpc-tls-cert)")
	root.PersistentFlags().StringVar(&cfg.dbDriver, "db-driver", envOrDefault("ARKEEP_DB_DRIVER", "sqlite"), "Database driver (sqlite or postgres)")
	root.PersistentFlags().StringVar(&cfg.dbDSN, "db-dsn", envOrDefault("ARKEEP_DB_DSN", "./arkeep.db"), "Database DSN or file path for SQLite")
	root.PersistentFlags().StringVar(&cfg.secretKey, "secret-key", envOrDefault("ARKEEP_SECRET_KEY", ""), "Master secret key for encrypting credentials at rest (required)")
	root.PersistentFlags().StringVar(&cfg.logLevel, "log-level", envOrDefault("ARKEEP_LOG_LEVEL", "info"), "Log level (debug, info, warn, error)")
	root.PersistentFlags().StringVar(&cfg.dataDir, "data-dir", envOrDefault("ARKEEP_DATA_DIR", "./data"), "Directory for server data (RSA keys, etc.)")
	root.PersistentFlags().StringVar(&cfg.agentSecret, "agent-secret", envOrDefault("ARKEEP_AGENT_SECRET", ""), "Shared secret for gRPC agent authentication (empty = disabled, dev only)")
root.PersistentFlags().BoolVar(&cfg.secureCookies, "secure-cookies", envOrDefault("ARKEEP_SECURE_COOKIES", "false") == "true", "Set Secure flag on auth cookies (enable in production over HTTPS)")
	root.PersistentFlags().BoolVar(&cfg.telemetry, "telemetry", envOrDefault("ARKEEP_TELEMETRY", "true") != "false", "Send anonymous usage stats (opt-out)")
	root.PersistentFlags().BoolVar(&cfg.grpcInsecure, "grpc-insecure", envOrDefault("ARKEEP_GRPC_INSECURE", "false") == "true", "Disable TLS for gRPC transport (development only — never use in production)")

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

	// Warn if agent secret is not configured — the gRPC port will accept
	// connections from any agent. Always set ARKEEP_AGENT_SECRET in production.
	if cfg.agentSecret == "" {
		logger.Warn("agent-secret not configured — gRPC port is open to any agent (set ARKEEP_AGENT_SECRET in production)")
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

	// --- Encryption ---
	// InitEncryption must be called before opening the database so that
	// EncryptedString fields can encrypt/decrypt transparently on read/write.
	// The key is derived via SHA-256 so that the full 256 bits of AES-256 are
	// always used regardless of the length or content of the secret key string.
	// A short secret (e.g. 8 chars) would otherwise leave most of the key as
	// zero bytes, dramatically reducing the effective brute-force resistance.
	keySum := sha256.Sum256([]byte(cfg.secretKey))
	if err := db.InitEncryption(keySum[:]); err != nil {
		return fmt.Errorf("failed to initialize encryption: %w", err)
	}

	// --- Database ---
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
	defer func() {
		if err := sqlDB.Close(); err != nil {
			logger.Warn("failed to close database", zap.Error(err))
		}
	}()

	// --- Repositories ---
	userRepo := repositories.NewUserRepository(gormDB)
	refreshTokenRepo := repositories.NewRefreshTokenRepository(gormDB)
	agentRepo := repositories.NewAgentRepository(gormDB)
	destinationRepo := repositories.NewDestinationRepository(gormDB)
	policyRepo := repositories.NewPolicyRepository(gormDB)
	jobRepo := repositories.NewJobRepository(gormDB)
	snapshotRepo := repositories.NewSnapshotRepository(gormDB)
	notificationRepo := repositories.NewNotificationRepository(gormDB)
	oidcProviderRepo := repositories.NewOIDCProviderRepository(gormDB)
	settingsRepo := repositories.NewSettingsRepository(gormDB)
	dashboardRepo := repositories.NewDashboardRepository(gormDB)

	// --- Auth ---
	// In development (no data dir or missing key files), ephemeral keys are
	// generated in memory. In production, persistent PEM files are used so
	// tokens survive server restarts.
	jwtManager, err := buildJWTManager(cfg.dataDir, logger)
	if err != nil {
		return fmt.Errorf("failed to initialize JWT manager: %w", err)
	}

	localProvider := auth.NewLocalAuthProvider(userRepo, refreshTokenRepo, jwtManager, logger)
	oidcProvider := auth.NewOIDCAuthProvider(oidcProviderRepo, userRepo, refreshTokenRepo, jwtManager, logger)
	authService := auth.NewAuthService(localProvider, oidcProvider, refreshTokenRepo, jwtManager)

	// --- gRPC PKI (auto-generated) ---
	// EnsureCerts is only called when no external TLS cert is configured.
	// When an external cert is provided (e.g. via Let's Encrypt / cert-manager),
	// the auto-PKI and the enrollment endpoint are skipped entirely.
	var autoCerts *grpcserver.AutoCerts
	if cfg.grpcTLSCert == "" && !cfg.grpcInsecure {
		autoCerts, err = grpcserver.EnsureCerts(cfg.dataDir, logger)
		if err != nil {
			return fmt.Errorf("failed to initialize gRPC PKI: %w", err)
		}
		logger.Info("gRPC CA cert available for agent enrollment",
			zap.String("ca_cert", autoCerts.CACertFile),
			zap.String("enroll_endpoint", "POST /api/v1/agents/enroll"),
		)
	}

	// --- Agent Manager ---
	agentMgr := agentmanager.New(logger)

	// --- Scheduler ---
	sched, err := scheduler.New(policyRepo, jobRepo, destinationRepo, agentMgr, logger)
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

	// --- WebSocket Hub ---
	// The hub must start before the HTTP server so clients can connect
	// immediately after the server is ready.
	wsHub := websocket.NewHub()
	go wsHub.Run(ctx)

	// --- Notification Service ---
	// Must be created after the hub so it can publish real-time in-app
	// notifications. The service is the single writer to the notifications
	// table and the single caller of hub.Publish on notification topics.
	notifService := notification.NewService(notification.Config{
		NotifRepo:    notificationRepo,
		UserRepo:     userRepo,
		SettingsRepo: settingsRepo,
		Hub:          wsHub,
		Logger:       logger,
	})

	// --- gRPC server ---
	grpcSrv := grpcserver.New(
		grpcserver.Config{
			SharedSecret: cfg.agentSecret,
			TLSCertFile:  cfg.grpcTLSCert,
			TLSKeyFile:   cfg.grpcTLSKey,
			AutoCerts:    autoCerts,
			NotifService: notifService,
		},
		agentMgr,
		agentRepo,
		jobRepo,
		snapshotRepo,
		wsHub,
		logger,
	)

	go func() {
		if err := grpcSrv.ListenAndServe(ctx, cfg.grpcAddr); err != nil {
			logger.Error("gRPC server error", zap.Error(err))
			cancel()
		}
	}()

	// --- HTTP router ---
	router := api.NewRouter(api.RouterConfig{
		AuthService:   authService,
		Scheduler:     sched,
		AgentManager:  agentMgr,
		Logger:        logger,
		Hub:           wsHub,
		Users:         userRepo,
		Agents:        agentRepo,
		Destinations:  destinationRepo,
		Policies:      policyRepo,
		Jobs:          jobRepo,
		Snapshots:     snapshotRepo,
		Notifications: notificationRepo,
		OIDCProviders: oidcProviderRepo,
		Settings:      settingsRepo,
		Secure:        cfg.secureCookies,
		Dashboard:     dashboardRepo,
		AutoCerts:     autoCerts,
		AgentSecret:   cfg.agentSecret,
	})
	api.MountGUI(router, guiFS())

	// --- HTTP server ---
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

	// --- Telemetry ---
	if cfg.telemetry {
		stats := &telemetryStats{agentMgr: agentMgr, policyRepo: policyRepo}
		reporter, firstRun, err := telemetry.New(version, cfg.dataDir, stats, logger)
		if err != nil {
			logger.Warn("telemetry: failed to initialize, disabling", zap.Error(err))
		} else {
			if firstRun {
				logger.Info("Arkeep collects anonymous usage statistics to help prioritize development.\n" +
					"No personal data, backup contents, or credentials are ever transmitted.\n" +
					"To opt out: set ARKEEP_TELEMETRY=false or --telemetry=false\n" +
					"Public stats: https://telemetry.arkeep.io/stats")
			}
			go reporter.Start(ctx)
		}
	}

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

// telemetryStats adapts agentmanager.Manager and repositories.PolicyRepository
// to the telemetry.StatsProvider interface without importing them from the
// telemetry package.
type telemetryStats struct {
	agentMgr   *agentmanager.Manager
	policyRepo repositories.PolicyRepository
}

func (s *telemetryStats) ConnectedAgentsCount() int { return s.agentMgr.ConnectedAgentsCount() }
func (s *telemetryStats) ActivePoliciesCount() int {
	return s.policyRepo.ActivePoliciesCount(context.Background())
}

// buildJWTManager loads RSA keys from the data directory if available,
// or generates ephemeral in-memory keys for development.
func buildJWTManager(dataDir string, logger *zap.Logger) (*auth.JWTManager, error) {
	privPath := filepath.Join(dataDir, "jwt_private.pem")
	pubPath := filepath.Join(dataDir, "jwt_public.pem")

	_, err := os.Stat(privPath)
	if err == nil {
		logger.Info("loading JWT keys from disk", zap.String("private", privPath))
		return auth.NewJWTManagerFromFiles(privPath, pubPath, "arkeep-server")
	}
	if !errors.Is(err, os.ErrNotExist) {
		// Stat failed for a reason other than "file not found" (e.g. permission
		// denied). Falling back to ephemeral keys here would silently discard
		// persistent tokens, so treat it as a hard error instead.
		return nil, fmt.Errorf("JWT key file inaccessible (%s): %w", privPath, err)
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