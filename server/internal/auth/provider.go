package auth

import "time"

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

	// CallbackURL is the redirect URI that was used in AuthorizationURL.
	// Must match exactly what was sent to the identity provider.
	CallbackURL string

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

	// AccessTokenExpiresAt is used by the HTTP layer to compute expires_in
	// in the response body so the frontend can schedule proactive refreshes.
	AccessTokenExpiresAt time.Time

	// RefreshToken is the raw opaque token string. The HTTP handler is
	// responsible for setting it as a cookie; this struct does not carry
	// cookie metadata (path, domain, SameSite) to keep the auth layer
	// decoupled from HTTP concerns.
	RefreshToken string

	// RefreshTokenExpiresAt is used by the HTTP layer to set the cookie
	// Max-Age / Expires attribute correctly.
	RefreshTokenExpiresAt time.Time
}