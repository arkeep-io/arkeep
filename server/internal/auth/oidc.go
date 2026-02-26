package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

const (
	// oidcStatBytes is the length of the random state parameter for CSRF protection.
	oidcStateBytes = 16

	// oidcCodeVerifierBytes is the length of the PKCE code verifier before encoding.
	// RFC 7636 requires a minimum of 32 bytes of entropy.
	oidcCodeVerifierBytes = 32
)

// OIDCAuthProvider implements OIDCFlowProvider using coreos/go-oidc.
// It handles the Authorization Code flow with PKCE for a single configured
// OIDC provider. The provider configuration is loaded from the database on
// each call to allow runtime updates without server restart.
//
// Dependency on OIDCProviderRepository (not a cached config struct) is
// intentional: OIDC provider settings can be updated via the admin UI and
// must be reflected immediately without a restart.
type OIDCAuthProvider struct {
	providerRepo repositories.OIDCProviderRepository
	userRepo     repositories.UserRepository
	tokenRepo    repositories.RefreshTokenRepository
	jwtManager   *JWTManager
}

// NewOIDCAuthProvider creates an OIDCAuthProvider with the given dependencies.
func NewOIDCAuthProvider(
	providerRepo repositories.OIDCProviderRepository,
	userRepo repositories.UserRepository,
	tokenRepo repositories.RefreshTokenRepository,
	jwtManager *JWTManager,
) *OIDCAuthProvider {
	return &OIDCAuthProvider{
		providerRepo: providerRepo,
		userRepo:     userRepo,
		tokenRepo:    tokenRepo,
		jwtManager:   jwtManager,
	}
}

// ProviderType implements AuthProvider.
func (p *OIDCAuthProvider) ProviderType() string {
	return "oidc"
}

// Login is not used for OIDC — the flow goes through AuthorizationURL and
// ExchangeCode. This satisfies the AuthProvider interface but always returns
// an error to prevent accidental misuse.
func (p *OIDCAuthProvider) Login(_ context.Context, _ LoginRequest) (*TokenPair, error) {
	return nil, fmt.Errorf("auth: Login is not supported for OIDC provider, use AuthorizationURL and ExchangeCode")
}

// AuthorizationURL generates the OIDC authorization URL with a random state
// parameter and PKCE code verifier. The caller must store state and
// codeVerifier in short-lived session cookies before redirecting the user.
func (p *OIDCAuthProvider) AuthorizationURL(ctx context.Context) (url, state, codeVerifier string, err error) {
	cfg, oauth2Cfg, err := p.loadConfig(ctx)
	if err != nil {
		return "", "", "", err
	}
	_ = cfg // provider config loaded for validation; oauth2Cfg carries the relevant fields

	state, err = generateRandomBase64(oidcStateBytes)
	if err != nil {
		return "", "", "", fmt.Errorf("auth: generating OIDC state: %w", err)
	}

	codeVerifier, err = generateRandomBase64(oidcCodeVerifierBytes)
	if err != nil {
		return "", "", "", fmt.Errorf("auth: generating PKCE code verifier: %w", err)
	}

	url = oauth2Cfg.AuthCodeURL(
		state,
		oauth2.AccessTypeOnline,
		oauth2.S256ChallengeOption(codeVerifier),
	)

	return url, state, codeVerifier, nil
}

// ExchangeCode completes the OIDC Authorization Code flow. It verifies the
// state parameter, exchanges the code for tokens, validates the ID token,
// and either retrieves the existing user or provisions a new one (JIT provisioning).
func (p *OIDCAuthProvider) ExchangeCode(ctx context.Context, req OIDCCallbackRequest) (*TokenPair, error) {
	if req.State != req.SessionState {
		return nil, ErrOIDCStateMismatch
	}

	if req.CodeVerifier == "" {
		return nil, ErrOIDCCodeVerifierMissing
	}

	cfg, oauth2Cfg, err := p.loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	// Exchange the authorization code for an OAuth2 token set.
	oauth2Token, err := oauth2Cfg.Exchange(
		ctx,
		req.Code,
		oauth2.VerifierOption(req.CodeVerifier),
	)
	if err != nil {
		return nil, fmt.Errorf("auth: exchanging OIDC code: %w", err)
	}

	// Extract and verify the ID token from the OAuth2 token response.
	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("auth: OIDC token response missing id_token")
	}

	oidcProvider, err := gooidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("auth: initializing OIDC provider for issuer %q: %w", cfg.Issuer, err)
	}

	verifier := oidcProvider.Verifier(&gooidc.Config{ClientID: cfg.ClientID})

	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("auth: verifying OIDC id_token: %w", err)
	}

	// Extract standard claims from the verified ID token.
	var claims struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("auth: extracting OIDC claims: %w", err)
	}

	user, err := p.findOrProvisionUser(ctx, cfg, claims.Sub, claims.Email, claims.Name)
	if err != nil {
		return nil, err
	}

	if !user.IsActive {
		return nil, ErrUserDisabled
	}

	return p.issueTokenPair(ctx, user.ID, user.Email, user.Role)
}

// RefreshToken delegates to the same logic as LocalAuthProvider — refresh
// tokens are provider-agnostic once issued.
func (p *OIDCAuthProvider) RefreshToken(ctx context.Context, rawToken string) (*TokenPair, error) {
	tokenHash := hashRefreshToken(rawToken)

	stored, err := p.tokenRepo.GetByHash(ctx, tokenHash)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrRefreshTokenNotFound
		}
		return nil, fmt.Errorf("auth: fetching refresh token: %w", err)
	}

	if err := p.tokenRepo.DeleteByHash(ctx, tokenHash); err != nil {
		return nil, fmt.Errorf("auth: deleting old refresh token: %w", err)
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

// Logout invalidates the given refresh token. No OIDC back-channel logout
// is performed — the session at the identity provider remains active.
func (p *OIDCAuthProvider) Logout(ctx context.Context, rawToken string) error {
	tokenHash := hashRefreshToken(rawToken)

	if err := p.tokenRepo.DeleteByHash(ctx, tokenHash); err != nil && !isNotFound(err) {
		return fmt.Errorf("auth: revoking refresh token on logout: %w", err)
	}

	return nil
}

// loadConfig retrieves the enabled OIDC provider from the database and builds
// the oauth2.Config. Called on every request so configuration changes are
// picked up without a server restart.
func (p *OIDCAuthProvider) loadConfig(ctx context.Context) (*db.OIDCProvider, *oauth2.Config, error) {
	cfg, err := p.providerRepo.GetEnabled(ctx)
	if err != nil {
		if isNotFound(err) {
			return nil, nil, ErrProviderNotFound
		}
		return nil, nil, fmt.Errorf("auth: loading OIDC provider config: %w", err)
	}

	oauth2Cfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: string(cfg.ClientSecret),
		RedirectURL:  cfg.RedirectURL,
		Endpoint: oauth2.Endpoint{
			AuthURL:  cfg.Issuer + "/authorize",
			TokenURL: cfg.Issuer + "/token",
		},
		Scopes: splitScopes(cfg.Scopes),
	}

	return cfg, oauth2Cfg, nil
}

// findOrProvisionUser looks up a user by OIDC subject claim. If no user exists,
// a new account is created (JIT provisioning) with role "user" by default.
// Email updates from the identity provider are applied on every login.
func (p *OIDCAuthProvider) findOrProvisionUser(ctx context.Context, cfg *db.OIDCProvider, sub, email, displayName string) (*db.User, error) {
	user, err := p.userRepo.GetByOIDC(ctx, cfg.ID.String(), sub)
	if err != nil && !isNotFound(err) {
		return nil, fmt.Errorf("auth: looking up OIDC user: %w", err)
	}

	if err == nil {
		// User exists — update email and display name in case they changed at the IdP.
		user.Email = email
		user.DisplayName = displayName
		if updateErr := p.userRepo.Update(ctx, user); updateErr != nil {
			// Non-fatal: log-worthy but should not block login.
			// The caller will use the stale data from the existing user record.
			_ = updateErr
		}
		return user, nil
	}

	// No existing user — provision a new account.
	newUser := &db.User{
		Email:        email,
		DisplayName:  displayName,
		Role:         "user",
		IsActive:     true,
		OIDCProvider: cfg.ID.String(),
		OIDCSub:      sub,
	}

	if err := p.userRepo.Create(ctx, newUser); err != nil {
		return nil, fmt.Errorf("auth: provisioning OIDC user: %w", err)
	}

	return newUser, nil
}

// issueTokenPair is the OIDC equivalent of LocalAuthProvider.issueTokenPair.
// Duplicated intentionally to keep the two providers independent — a shared
// helper would couple them through a common base type.
func (p *OIDCAuthProvider) issueTokenPair(ctx context.Context, userID uuid.UUID, email, role string) (*TokenPair, error) {
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

// generateRandomBase64 returns a URL-safe base64-encoded random string of n bytes.
func generateRandomBase64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// splitScopes splits a space-separated scopes string into a slice.
// Returns ["openid"] as a safe fallback if the input is empty.
func splitScopes(s string) []string {
	if s == "" {
		return []string{"openid"}
	}
	var scopes []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' {
			if i > start {
				scopes = append(scopes, s[start:i])
			}
			start = i + 1
		}
	}
	return scopes
}