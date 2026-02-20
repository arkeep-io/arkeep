package auth

import "errors"

// Sentinel errors returned by auth providers and the auth service.
// Callers should use errors.Is for comparison.
var (
	// ErrInvalidCredentials is returned when email/password do not match.
	ErrInvalidCredentials = errors.New("auth: invalid credentials")

	// ErrUserNotFound is returned when no user exists for the given identifier.
	ErrUserNotFound = errors.New("auth: user not found")

	// ErrUserDisabled is returned when the user account is inactive.
	ErrUserDisabled = errors.New("auth: user account is disabled")

	// ErrTokenExpired is returned when a JWT or refresh token has expired.
	ErrTokenExpired = errors.New("auth: token expired")

	// ErrTokenInvalid is returned when a token cannot be parsed or verified.
	ErrTokenInvalid = errors.New("auth: token invalid")

	// ErrRefreshTokenNotFound is returned when the provided refresh token
	// does not exist or has already been rotated out.
	ErrRefreshTokenNotFound = errors.New("auth: refresh token not found")

	// ErrProviderNotFound is returned when no OIDC provider matches the given ID.
	ErrProviderNotFound = errors.New("auth: oidc provider not found")

	// ErrOIDCStateMismatch is returned when the OAuth2 state parameter does
	// not match the value stored in the session cookie (CSRF protection).
	ErrOIDCStateMismatch = errors.New("auth: oidc state mismatch")

	// ErrOIDCCodeVerifierMissing is returned when the PKCE code verifier is
	// absent from the session during the callback phase.
	ErrOIDCCodeVerifierMissing = errors.New("auth: oidc code verifier missing")
)