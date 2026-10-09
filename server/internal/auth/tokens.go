package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// tokenIssuer issues, rotates and revokes token pairs. It is embedded by every
// auth provider so that refresh and logout behave the same whichever way the
// user signed in: a refresh token does not record which provider issued it.
type tokenIssuer struct {
	userRepo   repositories.UserRepository
	tokenRepo  repositories.RefreshTokenRepository
	jwtManager *JWTManager
}

// RefreshToken validates a refresh token, rotates it, and issues a new token pair.
// The old token is deleted before issuing the new one — if the issue fails the
// user must log in again. This prevents replay attacks even on partial failures.
func (p *tokenIssuer) RefreshToken(ctx context.Context, rawToken string) (*TokenPair, error) {
	tokenHash := hashRefreshToken(rawToken)

	stored, err := p.tokenRepo.GetByHash(ctx, tokenHash)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrRefreshTokenNotFound
		}
		return nil, fmt.Errorf("auth: fetching refresh token: %w", err)
	}

	// Delete before issuing the new pair — if issue fails the user must re-login.
	if err := p.tokenRepo.DeleteByHash(ctx, tokenHash); err != nil {
		return nil, fmt.Errorf("auth: deleting old refresh token: %w", err)
	}

	if time.Now().After(stored.ExpiresAt) {
		return nil, ErrTokenExpired
	}

	user, err := p.userRepo.GetByID(ctx, stored.UserID)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("auth: fetching user for token refresh: %w", err)
	}

	if !user.IsActive {
		return nil, ErrUserDisabled
	}

	return p.issueTokenPair(ctx, user.ID, user.Email, user.Role)
}

// Logout invalidates the given refresh token.
// If the token does not exist the call is a no-op — the client should clear
// its cookie regardless.
func (p *tokenIssuer) Logout(ctx context.Context, rawToken string) error {
	tokenHash := hashRefreshToken(rawToken)

	if err := p.tokenRepo.DeleteByHash(ctx, tokenHash); err != nil && !isNotFound(err) {
		return fmt.Errorf("auth: revoking refresh token on logout: %w", err)
	}

	return nil
}

// issueTokenPair generates a new access token and refresh token, persists the
// refresh token hash, and returns both as a TokenPair.
func (p *tokenIssuer) issueTokenPair(ctx context.Context, userID uuid.UUID, email, role string) (*TokenPair, error) {
	accessToken, err := p.jwtManager.GenerateAccessToken(userID.String(), email, role)
	if err != nil {
		return nil, err
	}

	rawRefresh, err := generateRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("auth: generating refresh token: %w", err)
	}

	expiresAt := time.Now().Add(refreshTokenDuration)

	if err := p.tokenRepo.Create(ctx, &db.RefreshToken{
		UserID:    userID,
		TokenHash: hashRefreshToken(rawRefresh),
		ExpiresAt: expiresAt,
	}); err != nil {
		return nil, fmt.Errorf("auth: persisting refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:           accessToken,
		AccessTokenExpiresAt:  time.Now().Add(accessTokenDuration),
		RefreshToken:          rawRefresh,
		RefreshTokenExpiresAt: expiresAt,
	}, nil
}
