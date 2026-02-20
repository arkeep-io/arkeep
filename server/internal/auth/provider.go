package auth

import (
	"context"
	"time"
)

// AuthProvider is the interface that every authentication backend must implement.
// Currently two implementations exist: LocalAuthProvider (email/password) and
// OIDCAuthProvider (external identity provider via OpenID Connect).
//
// New providers (SAML, LDAP, etc.) can be added by implementing this interface
// without changes to the auth service or API layer.
type AuthProvider interface {
	// Login authenticates a user and returns a token pair on success.
	// The access token is a signed JWT; the refresh token is an opaque string
	// that must be stored in an httpOnly cookie by the caller.
	Login(ctx context.Context, req LoginRequest) (*TokenPair, error)

	// RefreshToken validates a refresh token, rotates it, and returns a new
	// token pair. The old refresh token is invalidated after this call.
	RefreshToken(ctx context.Context, refreshToken string) (*TokenPair, error)

	// Logout invalidates the given refresh token so it cannot be used again.
	// Access tokens remain valid until expiry — their short TTL (15 min) is
	// the revocation mechanism for those.
	Logout(ctx context.Context, refreshToken string) error

	// ProviderType returns a string identifier for this provider.
	// Used for logging and to route OIDC callbacks to the correct provider.
	ProviderType() string
}

// OIDCFlowProvider extends AuthProvider with the two-step OAuth2 flow.
// Only OIDCAuthProvider implements this interface.
//
// The split from AuthProvider is intentional: the REST API layer can type-assert
// to OIDCFlowProvider when handling /auth/oidc/* routes, keeping the base
// AuthProvider interface clean and implementable by non-OIDC providers.
type OIDCFlowProvider interface {
	AuthProvider

	// AuthorizationURL generates the OIDC authorization URL and returns the
	// state and code verifier (PKCE) that must be stored server-side in a
	// short-lived session cookie before redirecting the user.
	AuthorizationURL(ctx context.Context) (url, state, codeVerifier string, err error)

	// ExchangeCode completes the OIDC flow by exchanging the authorization code
	// for tokens. state and codeVerifier must match the values from AuthorizationURL.
	ExchangeCode(ctx context.Context, req OIDCCallbackRequest) (*TokenPair, error)
}

// LoginRequest carries credentials for a local email/password login attempt.
// OIDC logins use OIDCCallbackRequest instead and bypass Login entirely.
type LoginRequest struct {
	Email    string
	Password string
}

// OIDCCallbackRequest carries the parameters received in the OAuth2 callback.
type OIDCCallbackRequest struct {
	// ProviderID identifies which OIDC provider configuration to use.
	ProviderID string

	// Code is the authorization code returned by the identity provider.
	Code string

	// State must match the value generated in AuthorizationURL (CSRF protection).
	State string

	// SessionState is the state value stored in the session cookie, used to
	// verify the State parameter from the identity provider.
	SessionState string

	// CodeVerifier is the PKCE verifier stored in the session cookie.
	CodeVerifier string
}

// TokenPair is returned after a successful login or token refresh.
// AccessToken is meant to be returned in the response body (or Authorization header).
// RefreshToken is meant to be set as an httpOnly Secure cookie by the HTTP layer —
// it is never included in API responses directly.
type TokenPair struct {
	AccessToken string

	// RefreshToken is the raw opaque token string. The HTTP handler is
	// responsible for setting it as a cookie; this struct does not carry
	// cookie metadata (path, domain, SameSite) to keep the auth layer
	// decoupled from HTTP concerns.
	RefreshToken string

	// RefreshTokenExpiresAt is used by the HTTP layer to set the cookie
	// Max-Age / Expires attribute correctly.
	RefreshTokenExpiresAt time.Time
}