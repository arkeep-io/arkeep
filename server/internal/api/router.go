package api

import (
	"database/sql"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/auth"
	grpccerts "github.com/arkeep-io/arkeep/server/internal/grpc"
	"github.com/arkeep-io/arkeep/server/internal/metrics"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/scheduler"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
)

// RouterConfig holds all dependencies needed to build the HTTP router.
type RouterConfig struct {
	AuthService  *auth.AuthService
	Scheduler    *scheduler.Scheduler
	AgentManager *agentmanager.Manager
	Logger       *zap.Logger
	Hub          *websocket.Hub

	// Repositories — used directly by handlers that do not need service-layer logic.
	Users         repositories.UserRepository
	Agents        repositories.AgentRepository
	Destinations  repositories.DestinationRepository
	Policies      repositories.PolicyRepository
	Jobs          repositories.JobRepository
	Snapshots     repositories.SnapshotRepository
	Notifications repositories.NotificationRepository
	OIDCProviders repositories.OIDCProviderRepository
	Settings      repositories.SettingsRepository
	Dashboard     repositories.DashboardRepository
	Audit         repositories.AuditRepository

	// Secure controls whether auth cookies are set with the Secure flag.
	Secure bool

	// AutoCerts is the auto-generated PKI used for gRPC mTLS enrollment.
	AutoCerts *grpccerts.AutoCerts

	// AgentSecret is the shared bootstrap secret agents must present when enrolling.
	AgentSecret string

	// ServerVersion is the running server binary version (injected at build time
	// via ldflags). Used by the version endpoint to report the current version
	// and check for updates.
	ServerVersion string

	// Metrics is the Prometheus metrics collector used to instrument HTTP
	// requests. Optional — if nil, HTTP metrics are not recorded.
	Metrics *metrics.Metrics

	// DB is the underlying sql.DB used by the /health/ready endpoint to
	// verify database reachability. Required for readiness checks.
	DB *sql.DB
}

// NewRouter builds and returns the fully configured Chi router.
func NewRouter(cfg RouterConfig) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(RequestLogger(cfg.Logger))
	r.Use(middleware.Recoverer)
	r.Use(SecurityHeaders)
	if cfg.Metrics != nil {
		r.Use(cfg.Metrics.HTTPMiddleware)
	}

	// --- Initialize handlers ---
	setupHandler        := NewSetupHandler(cfg.Users, cfg.Logger)
	authHandler         := NewAuthHandler(cfg.AuthService, cfg.Audit, cfg.Logger, cfg.Secure)
	var enrollHandler *EnrollHandler
	if cfg.AutoCerts != nil {
		enrollHandler = NewEnrollHandler(cfg.AutoCerts, cfg.AgentSecret, cfg.Logger)
	}
	agentHandler        := NewAgentHandler(cfg.Agents, cfg.AgentManager, cfg.Audit, cfg.Logger)
	destinationHandler  := NewDestinationHandler(cfg.Destinations, cfg.Audit, cfg.Logger)
	policyHandler       := NewPolicyHandler(cfg.Policies, cfg.Agents, cfg.Scheduler, cfg.Audit, cfg.Logger)
	jobHandler          := NewJobHandler(cfg.Jobs, cfg.Logger)
	snapshotHandler     := NewSnapshotHandler(cfg.Snapshots, cfg.Destinations, cfg.Policies, cfg.Jobs, cfg.AgentManager, cfg.Audit, cfg.Logger)
	userHandler         := NewUserHandler(cfg.Users, cfg.Audit, cfg.Logger)
	notificationHandler := NewNotificationHandler(cfg.Notifications, cfg.Logger)
	settingsHandler     := NewSettingsHandler(cfg.OIDCProviders, cfg.Settings, cfg.Audit, cfg.Logger)
	wsHandler           := NewWSHandler(cfg.Hub, cfg.AuthService, cfg.Logger)
	dashboardHandler    := NewDashboardHandler(cfg.Dashboard, cfg.Logger)
	versionHandler      := newVersionHandler(cfg.ServerVersion)
	auditHandler        := NewAuditHandler(cfg.Audit, cfg.Logger)

	healthHandler := newHealthHandler(cfg.DB, cfg.Scheduler)
	r.Get("/health/live", healthHandler.Live)
	r.Get("/health/ready", healthHandler.Ready)

	// Prometheus metrics endpoint — unauthenticated (protect at network level
	// or via a reverse proxy in production).
	r.Mount("/metrics", metrics.Handler())

	// OIDC callback — registered at the root so the redirect URI registered
	// with identity providers does not carry the /api/v1 prefix.
	r.Get("/auth/oidc/callback", authHandler.OIDCCallback)

	r.Route("/api/v1", func(r chi.Router) {

		// --- Public routes ---
		r.Group(func(r chi.Router) {
			// Login and refresh are rate-limited to 5 requests per minute per IP
			// to prevent brute-force attacks on credentials.
			loginLimiter := NewRateLimiter(5, time.Minute)
			r.With(RateLimit(loginLimiter)).Post("/auth/login", authHandler.Login)
			r.With(RateLimit(loginLimiter)).Post("/auth/refresh", authHandler.Refresh)

			// OIDC flow — public because the user is not yet authenticated.
			// /providers lists enabled providers for the login page SSO buttons.
			// /login?provider_id={id} initiates the flow for a specific provider.
			r.Get("/auth/oidc/providers", authHandler.ListOIDCProviders)
			r.Get("/auth/oidc/login", authHandler.OIDCLogin)

			r.Get("/setup/status", setupHandler.GetStatus)
			r.Post("/setup/complete", setupHandler.Complete)

			if enrollHandler != nil {
				r.Post("/agents/enroll", enrollHandler.Enroll)
			}

			r.Get("/ws", wsHandler.ServeWS)
		})

		// --- Authenticated routes ---
		r.Group(func(r chi.Router) {
			r.Use(Authenticate(cfg.AuthService))

			r.Get("/dashboard", dashboardHandler.Get)
			r.Get("/version", versionHandler.Get)

			r.Post("/auth/logout", authHandler.Logout)

			r.Get("/users/me", userHandler.GetMe)
			r.Patch("/users/me", userHandler.UpdateMe)

			// Agents
			r.Get("/agents", agentHandler.List)
			r.Post("/agents", agentHandler.Create)
			r.Get("/agents/{id}", agentHandler.GetByID)
			r.Patch("/agents/{id}", agentHandler.Update)
			r.With(RequireRole("admin")).Delete("/agents/{id}", agentHandler.Delete)
			r.Get("/agents/{id}/volumes", agentHandler.ListVolumes)

			// Destinations
			r.Get("/destinations", destinationHandler.List)
			r.Post("/destinations", destinationHandler.Create)
			r.Get("/destinations/{id}", destinationHandler.GetByID)
			r.Patch("/destinations/{id}", destinationHandler.Update)
			r.Delete("/destinations/{id}", destinationHandler.Delete)

			// Policies
			r.Get("/policies", policyHandler.List)
			r.Post("/policies", policyHandler.Create)
			r.Get("/policies/{id}", policyHandler.GetByID)
			r.Patch("/policies/{id}", policyHandler.Update)
			r.With(RequireRole("admin")).Delete("/policies/{id}", policyHandler.Delete)
			r.With(RequireRole("admin")).Post("/policies/{id}/trigger", policyHandler.Trigger)
			r.Get("/policies/{id}/jobs", jobHandler.ListByPolicy)

			// Jobs
			r.Get("/jobs", jobHandler.List)
			r.Get("/jobs/{id}", jobHandler.GetByID)
			r.Get("/jobs/{id}/logs", jobHandler.GetLogs)

			// Snapshots
			r.Get("/snapshots", snapshotHandler.List)
			r.Get("/snapshots/{id}", snapshotHandler.GetByID)
			r.With(RequireRole("admin")).Delete("/snapshots/{id}", snapshotHandler.Delete)
			r.With(RequireRole("admin")).Post("/snapshots/{id}/restore", snapshotHandler.Restore)

			// Notifications
			r.Get("/notifications", notificationHandler.List)
			r.Patch("/notifications/{id}/read", notificationHandler.MarkAsRead)
			r.Patch("/notifications/read-all", notificationHandler.MarkAllAsRead)

			// --- Admin-only routes ---
			r.Group(func(r chi.Router) {
				r.Use(RequireRole("admin"))

				// User management
				r.Get("/users", userHandler.List)
				r.Post("/users", userHandler.Create)
				r.Get("/users/{id}", userHandler.GetByID)
				r.Patch("/users/{id}", userHandler.Update)
				r.Delete("/users/{id}", userHandler.Delete)

				// OIDC provider configuration (multiple providers supported)
				r.Get("/settings/oidc", settingsHandler.ListOIDC)
				r.Post("/settings/oidc", settingsHandler.CreateOIDC)
				r.Get("/settings/oidc/{id}", settingsHandler.GetOIDCByID)
				r.Put("/settings/oidc/{id}", settingsHandler.UpdateOIDC)
				r.Delete("/settings/oidc/{id}", settingsHandler.DeleteOIDC)

				// SMTP configuration
				r.Get("/settings/smtp", settingsHandler.GetSMTP)
				r.Put("/settings/smtp", settingsHandler.UpsertSMTP)

				// Notification delivery queue visibility
				r.Get("/notifications/queue", notificationHandler.ListDeliveryQueue)

				// Audit log — read-only, append-only records of admin mutations
				r.Get("/audit", auditHandler.List)
			})
		})
	})

	return r
}
