package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/auth"
	grpccerts "github.com/arkeep-io/arkeep/server/internal/grpc"
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
}

// NewRouter builds and returns the fully configured Chi router.
func NewRouter(cfg RouterConfig) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(RequestLogger(cfg.Logger))
	r.Use(middleware.Recoverer)
	r.Use(SecurityHeaders)

	// --- Initialize handlers ---
	setupHandler        := NewSetupHandler(cfg.Users, cfg.Logger)
	authHandler         := NewAuthHandler(cfg.AuthService, cfg.Logger, cfg.Secure)
	var enrollHandler *EnrollHandler
	if cfg.AutoCerts != nil {
		enrollHandler = NewEnrollHandler(cfg.AutoCerts, cfg.AgentSecret, cfg.Logger)
	}
	agentHandler        := NewAgentHandler(cfg.Agents, cfg.AgentManager, cfg.Logger)
	destinationHandler  := NewDestinationHandler(cfg.Destinations, cfg.Logger)
	policyHandler       := NewPolicyHandler(cfg.Policies, cfg.Agents, cfg.Scheduler, cfg.Logger)
	jobHandler          := NewJobHandler(cfg.Jobs, cfg.Logger)
	snapshotHandler     := NewSnapshotHandler(cfg.Snapshots, cfg.Destinations, cfg.Policies, cfg.Jobs, cfg.AgentManager, cfg.Logger)
	userHandler         := NewUserHandler(cfg.Users, cfg.Logger)
	notificationHandler := NewNotificationHandler(cfg.Notifications, cfg.Logger)
	settingsHandler     := NewSettingsHandler(cfg.OIDCProviders, cfg.Settings, cfg.Logger)
	wsHandler           := NewWSHandler(cfg.Hub, cfg.AuthService.JWTManager(), cfg.Logger)
	dashboardHandler    := NewDashboardHandler(cfg.Dashboard, cfg.Logger)
	versionHandler      := newVersionHandler(cfg.ServerVersion)

	jwtMgr := cfg.AuthService.JWTManager()

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	})

	// OIDC callback — registered at the root so the redirect URI registered
	// with identity providers does not carry the /api/v1 prefix.
	r.Get("/auth/oidc/callback", authHandler.OIDCCallback)

	r.Route("/api/v1", func(r chi.Router) {

		// --- Public routes ---
		r.Group(func(r chi.Router) {
			r.Post("/auth/login", authHandler.Login)
			r.Post("/auth/refresh", authHandler.Refresh)

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
			r.Use(Authenticate(jwtMgr))

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
			r.Delete("/agents/{id}", agentHandler.Delete)
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
			r.Delete("/policies/{id}", policyHandler.Delete)
			r.Post("/policies/{id}/trigger", policyHandler.Trigger)
			r.Get("/policies/{id}/jobs", jobHandler.ListByPolicy)

			// Jobs
			r.Get("/jobs", jobHandler.List)
			r.Get("/jobs/{id}", jobHandler.GetByID)
			r.Get("/jobs/{id}/logs", jobHandler.GetLogs)

			// Snapshots
			r.Get("/snapshots", snapshotHandler.List)
			r.Get("/snapshots/{id}", snapshotHandler.GetByID)
			r.Delete("/snapshots/{id}", snapshotHandler.Delete)
			r.Post("/snapshots/{id}/restore", snapshotHandler.Restore)

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
			})
		})
	})

	return r
}
