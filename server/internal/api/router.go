package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/repository"
	"github.com/arkeep-io/arkeep/server/internal/scheduler"
)

// RouterConfig holds all dependencies needed to build the HTTP router.
// It is populated in main.go after all components are initialized and
// passed to NewRouter as a single struct to keep the constructor signature
// manageable as the number of dependencies grows.
type RouterConfig struct {
	AuthService *auth.AuthService
	Scheduler   *scheduler.Scheduler
	Logger      *zap.Logger

	// Repositories — used directly by handlers that do not need service-layer logic.
	Users         repository.UserRepository
	Agents        repository.AgentRepository
	Destinations  repository.DestinationRepository
	Policies      repository.PolicyRepository
	Jobs          repository.JobRepository
	Snapshots     repository.SnapshotRepository
	Notifications repository.NotificationRepository
	OIDCProviders repository.OIDCProviderRepository

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
	authHandler := NewAuthHandler(cfg.AuthService, cfg.Logger, cfg.Secure)
	agentHandler := NewAgentHandler(cfg.Agents, cfg.Logger)
	destinationHandler := NewDestinationHandler(cfg.Destinations, cfg.Logger)
	policyHandler := NewPolicyHandler(cfg.Policies, cfg.Scheduler, cfg.Logger)
	jobHandler := NewJobHandler(cfg.Jobs, cfg.Logger)
	snapshotHandler := NewSnapshotHandler(cfg.Snapshots, cfg.Logger)
	userHandler := NewUserHandler(cfg.Users, cfg.Logger)
	notificationHandler := NewNotificationHandler(cfg.Notifications, cfg.Logger)
	settingsHandler := NewSettingsHandler(cfg.OIDCProviders, cfg.Logger)

	// jwtMgr is used by the Authenticate middleware to validate Bearer tokens.
	jwtMgr := cfg.AuthService.JWTManager()

	r.Route("/api/v1", func(r chi.Router) {

		// --- Public routes (no authentication required) ---
		r.Group(func(r chi.Router) {
			r.Post("/auth/login", authHandler.Login)
			r.Post("/auth/refresh", authHandler.Refresh)

			// OIDC flow — public because the user is not yet authenticated.
			r.Get("/auth/oidc/login", authHandler.OIDCLogin)
			r.Get("/auth/oidc/callback", authHandler.OIDCCallback)
		})

		// --- Authenticated routes (valid JWT required) ---
		r.Group(func(r chi.Router) {
			r.Use(Authenticate(jwtMgr))

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
			})
		})
	})

	return r
}