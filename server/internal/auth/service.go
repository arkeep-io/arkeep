package auth

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// AuthService is the entry point for all authentication operations.
// It holds references to both providers and delegates to the appropriate one
// based on the operation requested.
//
// The REST API layer depends on AuthService, never on individual providers directly.
type AuthService struct {
	local      *LocalAuthProvider
	oidc       *OIDCAuthProvider
	tokenRepo  repositories.RefreshTokenRepository
	jwtManager *JWTManager
}

// NewAuthService creates an AuthService with the given providers and dependencies.
// Both local and oidc providers are required even if OIDC is not configured —
// OIDCAuthProvider will return ErrProviderNotFound at runtime if the provider
// ID does not exist in the database.
func NewAuthService(
	local *LocalAuthProvider,
	oidc *OIDCAuthProvider,
	tokenRepo repositories.RefreshTokenRepository,
	jwtManager *JWTManager,
) *AuthService {
	return &AuthService{
		local:      local,
		oidc:       oidc,
		tokenRepo:  tokenRepo,
		jwtManager: jwtManager,
	}
}

// LoginLocal authenticates a user via email and password.
func (s *AuthService) LoginLocal(ctx context.Context, req LoginRequest) (*TokenPair, error) {
	return s.local.Login(ctx, req)
}

// AuthorizationURL generates the OIDC authorization URL for the given provider.
// callbackURL is the server-computed redirect URI (base_url + /api/v1/auth/oidc/callback).
// Returns the URL to redirect the user to, plus state and codeVerifier that the
// caller must store in short-lived session cookies before redirecting.
func (s *AuthService) AuthorizationURL(ctx context.Context, providerID uuid.UUID, callbackURL string) (url, state, codeVerifier string, err error) {
	return s.oidc.AuthorizationURL(ctx, providerID, callbackURL)
}

// ExchangeCode completes the OIDC Authorization Code flow and returns a token pair.
func (s *AuthService) ExchangeCode(ctx context.Context, req OIDCCallbackRequest) (*TokenPair, error) {
	return s.oidc.ExchangeCode(ctx, req)
}

// ListEnabledProviders returns all enabled OIDC provider configurations.
// Used by the public login endpoint to build the per-provider SSO button list.
func (s *AuthService) ListEnabledProviders(ctx context.Context) ([]*db.OIDCProvider, error) {
	return s.oidc.ListEnabledProviders(ctx)
}

// RefreshToken validates and rotates a refresh token issued by either provider.
// Refresh tokens are provider-agnostic once issued.
func (s *AuthService) RefreshToken(ctx context.Context, rawToken string) (*TokenPair, error) {
	return s.local.RefreshToken(ctx, rawToken)
}

// Logout invalidates the given refresh token.
func (s *AuthService) Logout(ctx context.Context, rawToken string) error {
	return s.local.Logout(ctx, rawToken)
}

// LogoutAllSessions revokes all active refresh tokens for a user.
func (s *AuthService) LogoutAllSessions(ctx context.Context, userID uuid.UUID) error {
	if err := s.tokenRepo.RevokeAllForUser(ctx, userID); err != nil {
		return fmt.Errorf("auth: revoking all sessions for user %s: %w", userID, err)
	}
	return nil
}

// ValidateAccessToken parses and verifies a JWT access token.
func (s *AuthService) ValidateAccessToken(tokenString string) (*Claims, error) {
	return s.jwtManager.ValidateAccessToken(tokenString)
}

// JWTManager exposes the underlying JWTManager for direct access.
func (s *AuthService) JWTManager() *JWTManager {
	return s.jwtManager
}
