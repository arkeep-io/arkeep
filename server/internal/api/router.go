package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/scheduler"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
)

// RouterConfig holds all dependencies needed to build the HTTP router.
// It is populated in main.go after all components are initialized and
// passed to NewRouter as a single struct to keep the constructor signature
// manageable as the number of dependencies grows.
type RouterConfig struct {
	AuthService  *auth.AuthService
	Scheduler    *scheduler.Scheduler
	AgentManager *agentmanager.Manager
	Logger       *zap.Logger
	Hub          *websocket.Hub

	// Repositories — used directly by handlers that do not need service-layer logic.
	// Users is also needed by the public setup handler (no auth middleware).
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
	// Set to true in production (HTTPS), false in local development.
	Secure bool
}

// NewRouter builds and returns the fully configured Chi router.
// All routes are registered under /api/v1. The GUI is served as a catch-all
// from the root — this is wired in main.go after embedding the frontend assets.
func NewRouter(cfg RouterConfig) http.Handler {
	r := chi.NewRouter()

	// --- Global middleware ---
	// RequestID generates a unique ID for each request, used in logs and
	// response headers for tracing.
	r.Use(middleware.RequestID)

	// RealIP extracts the real client IP from X-Forwarded-For or X-Real-IP
	// headers when the server runs behind a reverse proxy.
	r.Use(middleware.RealIP)

	// RequestLogger logs every request with method, path, status and latency.
	r.Use(RequestLogger(cfg.Logger))

	// Recoverer catches panics in handlers, logs them, and returns a 500
	// instead of crashing the server.
	r.Use(middleware.Recoverer)

	// --- Initialize handlers ---
	setupHandler        := NewSetupHandler(cfg.Users, cfg.Logger)
	authHandler         := NewAuthHandler(cfg.AuthService, cfg.Logger, cfg.Secure)
	agentHandler        := NewAgentHandler(cfg.Agents, cfg.AgentManager, cfg.Logger)
	destinationHandler  := NewDestinationHandler(cfg.Destinations, cfg.Logger)
	policyHandler       := NewPolicyHandler(cfg.Policies, cfg.Agents, cfg.Scheduler, cfg.Logger)
	jobHandler          := NewJobHandler(cfg.Jobs, cfg.Logger)
	snapshotHandler     := NewSnapshotHandler(cfg.Snapshots, cfg.Logger)
	userHandler         := NewUserHandler(cfg.Users, cfg.Logger)
	notificationHandler := NewNotificationHandler(cfg.Notifications, cfg.Logger)
	settingsHandler     := NewSettingsHandler(cfg.OIDCProviders, cfg.Settings, cfg.Logger)
	wsHandler           := NewWSHandler(cfg.Hub, cfg.AuthService.JWTManager(), cfg.Logger)
	dashboardHandler    := NewDashboardHandler(cfg.Dashboard, cfg.Logger)

	// jwtMgr is used by the Authenticate middleware to validate Bearer tokens.
	jwtMgr := cfg.AuthService.JWTManager()

	// /health — unauthenticated liveness probe used by Docker healthchecks
	// and load balancers to verify the server process is running and responsive.
	// Returns 200 OK with a plain-text body; no database check is performed
	// so the endpoint remains fast even under load.
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	})

	r.Route("/api/v1", func(r chi.Router) {

		// --- Public routes (no authentication required) ---
		r.Group(func(r chi.Router) {
			r.Post("/auth/login", authHandler.Login)
			r.Post("/auth/refresh", authHandler.Refresh)

			// OIDC flow — public because the user is not yet authenticated.
			r.Get("/auth/oidc/login", authHandler.OIDCLogin)
			r.Get("/auth/oidc/callback", authHandler.OIDCCallback)

			// Setup — public because no users exist yet when these are called.
			// GetStatus is a lightweight check (COUNT query) safe to call on
			// every app load. Complete is self-sealing: it returns 409 once any
			// user exists, so it cannot be used as a backdoor after first run.
			r.Get("/setup/status", setupHandler.GetStatus)
			r.Post("/setup/complete", setupHandler.Complete)

			// WebSocket — authenticated via JWT query parameter (browsers cannot
			// set Authorization headers on native WebSocket connections).
			// The Authenticate middleware is NOT used here because the upgrade
			// must complete before any response is written.
			r.Get("/ws", wsHandler.ServeWS)
		})

		// --- Authenticated routes (valid JWT required) ---
		r.Group(func(r chi.Router) {
			r.Use(Authenticate(jwtMgr))

			// Dashboard — single aggregated endpoint for the overview page.
			r.Get("/dashboard", dashboardHandler.Get)

			// Auth
			r.Post("/auth/logout", authHandler.Logout)

			// Current user profile
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

				// OIDC provider configuration
				r.Get("/settings/oidc", settingsHandler.GetOIDC)
				r.Put("/settings/oidc", settingsHandler.UpsertOIDC)

				// SMTP configuration
				r.Get("/settings/smtp", settingsHandler.GetSMTP)
				r.Put("/settings/smtp", settingsHandler.UpsertSMTP)
			})
		})
	})

	return r
}