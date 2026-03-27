package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/oauth2"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

const (
	// oidcStateBytes is the length of the random state parameter for CSRF protection.
	oidcStateBytes = 16

	// oidcCodeVerifierBytes is the length of the PKCE code verifier before encoding.
	// RFC 7636 requires a minimum of 32 bytes of entropy.
	oidcCodeVerifierBytes = 32
)

// OIDCAuthProvider implements OIDCFlowProvider using coreos/go-oidc.
// It handles the Authorization Code flow with PKCE for multiple configured
// OIDC providers. Provider configuration is loaded from the database on each
// call to allow runtime updates without server restart.
type OIDCAuthProvider struct {
	providerRepo repositories.OIDCProviderRepository
	userRepo     repositories.UserRepository
	tokenRepo    repositories.RefreshTokenRepository
	jwtManager   *JWTManager
	logger       *zap.Logger
}

// NewOIDCAuthProvider creates an OIDCAuthProvider with the given dependencies.
func NewOIDCAuthProvider(
	providerRepo repositories.OIDCProviderRepository,
	userRepo repositories.UserRepository,
	tokenRepo repositories.RefreshTokenRepository,
	jwtManager *JWTManager,
	logger *zap.Logger,
) *OIDCAuthProvider {
	return &OIDCAuthProvider{
		providerRepo: providerRepo,
		userRepo:     userRepo,
		tokenRepo:    tokenRepo,
		jwtManager:   jwtManager,
		logger:       logger.Named("oidc_auth"),
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

// AuthorizationURL generates the OIDC authorization URL for the given provider.
// callbackURL is the redirect URI registered with the identity provider
// (computed server-side as {base_url}/api/v1/auth/oidc/callback).
// The caller must store state and codeVerifier in short-lived session cookies
// before redirecting the user.
func (p *OIDCAuthProvider) AuthorizationURL(ctx context.Context, providerID uuid.UUID, callbackURL string) (url, state, codeVerifier string, err error) {
	cfg, oauth2Cfg, err := p.loadConfig(ctx, providerID, callbackURL)
	if err != nil {
		return "", "", "", err
	}
	_ = cfg

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

	providerID, err := uuid.Parse(req.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("auth: invalid provider ID %q: %w", req.ProviderID, err)
	}

	cfg, oauth2Cfg, err := p.loadConfig(ctx, providerID, req.CallbackURL)
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

	// Use the discovered provider to verify the ID token signature and claims.
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

	// Many providers (e.g. Zitadel, Keycloak) only guarantee sub in the ID
	// token and return email/name via the UserInfo endpoint. Fetch UserInfo as
	// a fallback whenever either field is missing.
	if claims.Email == "" || claims.Name == "" {
		userInfo, uiErr := oidcProvider.UserInfo(ctx, oauth2.StaticTokenSource(oauth2Token))
		if uiErr != nil {
			p.logger.Warn("OIDC userinfo fetch failed, proceeding with ID token claims only",
				zap.Error(uiErr))
		} else {
			var uiClaims struct {
				Email string `json:"email"`
				Name  string `json:"name"`
			}
			if uiErr = userInfo.Claims(&uiClaims); uiErr == nil {
				if claims.Email == "" {
					claims.Email = uiClaims.Email
				}
				if claims.Name == "" {
					claims.Name = uiClaims.Name
				}
			}
		}
	}

	// sub is guaranteed by OIDC spec; email is required to provision an account.
	if claims.Sub == "" {
		return nil, fmt.Errorf("auth: OIDC id_token missing required 'sub' claim")
	}
	if claims.Email == "" {
		return nil, fmt.Errorf("auth: identity provider did not return an email address — ensure the 'email' scope is requested and the provider is configured to include it")
	}
	// Fall back to sub as display name if the provider does not return one.
	if claims.Name == "" {
		claims.Name = claims.Sub
	}

	user, err := p.findOrProvisionUser(ctx, cfg, claims.Sub, claims.Email, claims.Name)
	if err != nil {
		return nil, err
	}

	if !user.IsActive {
		return nil, ErrUserDisabled
	}

	// Update LastLoginAt. Non-fatal: a failure here should not block the login.
	now := time.Now()
	user.LastLoginAt = &now
	if err := p.userRepo.Update(ctx, user); err != nil {
		p.logger.Warn("failed to update LastLoginAt on OIDC login",
			zap.String("user_id", user.ID.String()),
			zap.Error(err),
		)
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

// ListEnabledProviders returns all enabled OIDC provider configurations.
// Used by the public login endpoint to build the SSO button list.
func (p *OIDCAuthProvider) ListEnabledProviders(ctx context.Context) ([]*db.OIDCProvider, error) {
	return p.providerRepo.ListEnabled(ctx)
}

// loadConfig retrieves the OIDC provider by ID from the database and builds
// the oauth2.Config using OIDC discovery (/.well-known/openid-configuration).
// Called on every request so configuration changes are picked up without a restart.
func (p *OIDCAuthProvider) loadConfig(ctx context.Context, providerID uuid.UUID, callbackURL string) (*db.OIDCProvider, *oauth2.Config, error) {
	cfg, err := p.providerRepo.GetByID(ctx, providerID)
	if err != nil {
		if isNotFound(err) {
			return nil, nil, ErrProviderNotFound
		}
		return nil, nil, fmt.Errorf("auth: loading OIDC provider config: %w", err)
	}

	// Use OIDC discovery to obtain the correct authorization and token endpoints.
	// This replaces the previous hard-coded {issuer}/authorize and {issuer}/token
	// pattern which fails for providers like Zitadel that use different paths.
	oidcProvider, err := gooidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, nil, fmt.Errorf("auth: OIDC discovery for issuer %q: %w", cfg.Issuer, err)
	}

	oauth2Cfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: string(cfg.ClientSecret),
		RedirectURL:  callbackURL,
		Endpoint:     oidcProvider.Endpoint(),
		Scopes:       splitScopes(cfg.Scopes),
	}

	return cfg, oauth2Cfg, nil
}

// findOrProvisionUser resolves the arkeep user for an incoming OIDC login.
//
// Lookup order:
//  1. By (oidc_provider, oidc_sub) — returning OIDC user, fast path.
//  2. By email — account linking: an existing local (or other-provider) account
//     with the same email is adopted for this OIDC provider so that no duplicate
//     is created.
//  3. Neither found — JIT-provision a new account with role "user".
//
// Email and display name are synced from the IdP on every login.
func (p *OIDCAuthProvider) findOrProvisionUser(ctx context.Context, cfg *db.OIDCProvider, sub, email, displayName string) (*db.User, error) {
	// 1. Fast path: returning OIDC user.
	user, err := p.userRepo.GetByOIDC(ctx, cfg.ID.String(), sub)
	if err != nil && !isNotFound(err) {
		return nil, fmt.Errorf("auth: looking up OIDC user: %w", err)
	}

	if isNotFound(err) {
		// 2. Account linking: look up by email.
		user, err = p.userRepo.GetByEmail(ctx, email)
		if err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("auth: looking up user by email: %w", err)
		}

		if isNotFound(err) {
			// 3. New user — provision.
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

		// Existing account found by email — link it to this OIDC provider.
		p.logger.Info("linking existing account to OIDC provider",
			zap.String("user_id", user.ID.String()),
			zap.String("provider", cfg.ID.String()),
		)
		user.OIDCProvider = cfg.ID.String()
		user.OIDCSub = sub
	}

	// Update email and display name in case they changed at the IdP.
	user.Email = email
	user.DisplayName = displayName
	if updateErr := p.userRepo.Update(ctx, user); updateErr != nil {
		p.logger.Warn("failed to update user profile from OIDC claims",
			zap.String("user_id", user.ID.String()),
			zap.Error(updateErr),
		)
	}
	return user, nil
}

// issueTokenPair is the OIDC equivalent of LocalAuthProvider.issueTokenPair.
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
