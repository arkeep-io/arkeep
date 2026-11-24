package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/db"
)

// contextKey is an unexported type for context keys defined in this package.
// Using a custom type prevents collisions with keys defined in other packages.
type contextKey int

const (
	// contextKeyUser is the context key under which the authenticated
	// *auth.Claims are stored after successful JWT validation.
	contextKeyUser contextKey = iota

	// contextKeyAPIToken holds the *db.APIToken of a request authenticated
	// with an API token rather than a session.
	contextKeyAPIToken
)

// APITokenAuthenticator validates API tokens (ADR 0027). Satisfied by
// *auth.APITokenValidator.
type APITokenAuthenticator interface {
	ValidateAPIToken(ctx context.Context, raw string) (*auth.Claims, *db.APIToken, error)
}

// Authenticate is a middleware that validates the Bearer credential present
// in the Authorization header. On success it stores the parsed claims in the
// request context so downstream handlers can retrieve them via claimsFromCtx.
// On failure it writes a 401 and stops the chain.
//
// Token format: "Authorization: Bearer <token>"
//
// The credential is either a JWT access token from a session or, when
// apiTokens is not nil, an API token ("ark_..."). validator should be
// *auth.AuthService in production — it performs both signature verification
// and denylist checking (revoked tokens are rejected).
//
// API tokens are read-only (ADR 0027): their claims carry the "user" role
// whatever the owner's role, and any method other than GET is refused with
// 403, so a token can never change anything even on a route open to the
// "user" role, such as marking notifications as read.
func Authenticate(validator auth.TokenValidator, apiTokens APITokenAuthenticator) func(http.Handler) http.Handler {
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

			if auth.IsAPIToken(parts[1]) {
				if apiTokens == nil {
					ErrUnauthorized(w)
					return
				}
				claims, token, err := apiTokens.ValidateAPIToken(r.Context(), parts[1])
				if err != nil {
					ErrUnauthorized(w)
					return
				}
				if r.Method != http.MethodGet {
					errJSON(w, http.StatusForbidden, "API tokens are read-only", "api_token_read_only")
					return
				}
				ctx := context.WithValue(r.Context(), contextKeyUser, claims)
				ctx = context.WithValue(ctx, contextKeyAPIToken, token)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			claims, err := validator.ValidateAccessToken(parts[1])
			if err != nil {
				ErrUnauthorized(w)
				return
			}

			ctx := context.WithValue(r.Context(), contextKeyUser, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// apiTokenFromCtx returns the API token that authenticated the request, or
// nil for a session.
func apiTokenFromCtx(ctx context.Context) *db.APIToken {
	t, _ := ctx.Value(contextKeyAPIToken).(*db.APIToken)
	return t
}

// RequireSession refuses requests authenticated with an API token. It guards
// the routes that manage API tokens: a token must never be able to list,
// create or revoke tokens, even read-only. It must be used after
// Authenticate.
func RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if apiTokenFromCtx(r.Context()) != nil {
			errJSON(w, http.StatusForbidden, "API tokens cannot manage API tokens", "api_token_forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
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

// isAdmin reports whether the authenticated user in r has the "admin" role.
// Must be called after the Authenticate middleware has run.
func isAdmin(r *http.Request) bool {
	claims := claimsFromCtx(r.Context())
	return claims != nil && claims.Role == "admin"
}

// SecurityHeaders adds defensive HTTP response headers to every request.
// These headers are a low-cost defense-in-depth measure against common
// web attacks (clickjacking, MIME sniffing, information leakage, etc.).
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// Prevent MIME-type sniffing — browsers must respect Content-Type.
		h.Set("X-Content-Type-Options", "nosniff")
		// Disallow embedding in iframes to prevent clickjacking.
		h.Set("X-Frame-Options", "DENY")
		// Limit referrer information sent to third-party origins.
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Restrict browser features not needed by this app.
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		// Keep search engines from indexing any response, the API and the
		// GUI's assets included: the meta tag in index.html covers only HTML.
		h.Set("X-Robots-Tag", "noindex, nofollow")
		next.ServeHTTP(w, r)
	})
}

// NoStore forbids browsers and proxies from storing the response. API
// responses are live state (or authenticated data): a stored copy would show
// stale state, e.g. a password reset status taken before SMTP was configured.
func NoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}