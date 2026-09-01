package auth

import (
	"errors"

	"github.com/google/uuid"
)

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

	// ErrTokenRevoked is returned when a syntactically valid access token has
	// been explicitly revoked via the denylist (e.g. after logout).
	ErrTokenRevoked = errors.New("auth: token has been revoked")
)

// TwoFactorRequiredError is returned by LocalAuthProvider.Login when the
// password is correct but the account has two-factor authentication enabled.
// It carries the user ID so the handler can create a challenge without
// re-reading the user. Authentication is not complete: no token is issued and
// LastLoginAt is not stamped.
type TwoFactorRequiredError struct {
	UserID uuid.UUID
}

func (e *TwoFactorRequiredError) Error() string {
	return "two-factor authentication required"
}