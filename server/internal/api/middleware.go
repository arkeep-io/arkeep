package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/auth"
)

// contextKey is an unexported type for context keys defined in this package.
// Using a custom type prevents collisions with keys defined in other packages.
type contextKey int

const (
	// contextKeyUser is the context key under which the authenticated
	// *auth.Claims are stored after successful JWT validation.
	contextKeyUser contextKey = iota
)

// Authenticate is a middleware that validates the JWT Bearer token present in
// the Authorization header. On success it stores the parsed claims in the
// request context so downstream handlers can retrieve them via claimsFromCtx.
// On failure it writes a 401 and stops the chain.
//
// Token format: "Authorization: Bearer <token>"
func Authenticate(jwtMgr *auth.JWTManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if header == "" {
				ErrUnauthorized(w)
				return
			}

			parts := strings.SplitN(header, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				ErrUnauthorized(w)
				return
			}

			claims, err := jwtMgr.ValidateAccessToken(parts[1])
			if err != nil {
				ErrUnauthorized(w)
				return
			}

			ctx := context.WithValue(r.Context(), contextKeyUser, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireRole returns a middleware that allows the request to proceed only if
// the authenticated user has the specified role. It must be used after
// Authenticate in the middleware chain, since it reads claims from context.
//
// Usage:
//
//	r.With(RequireRole("admin")).Get("/users", listUsers)
func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := claimsFromCtx(r.Context())
			if claims == nil {
				// Should never happen if Authenticate runs first.
				ErrUnauthorized(w)
				return
			}
			if claims.Role != role {
				ErrForbidden(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequestLogger returns a Chi-compatible middleware that logs each request
// using the provided zap logger. It logs method, path, status, and latency.
// Chi's middleware.RequestID is expected to run before this middleware so
// that the request ID is available in the context.
func RequestLogger(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			logger.Info("http request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", ww.Status()),
				zap.Int("bytes", ww.BytesWritten()),
				zap.String("request_id", middleware.GetReqID(r.Context())),
				zap.String("remote_addr", r.RemoteAddr),
			)
		})
	}
}

// claimsFromCtx retrieves the JWT claims stored by the Authenticate middleware.
// Returns nil if no claims are present (i.e. the request is unauthenticated).
// Handler functions use this to access the current user's ID and role.
func claimsFromCtx(ctx context.Context) *auth.Claims {
	claims, _ := ctx.Value(contextKeyUser).(*auth.Claims)
	return claims
}