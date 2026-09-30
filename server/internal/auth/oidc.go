package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
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

// oidcIdentity is the verified identity returned by the identity provider.
type oidcIdentity struct {
	sub, email, name string
	groups           []string
}

// ExchangeCode completes the OIDC Authorization Code flow. It verifies the
// callback, then either retrieves the existing user or provisions a new one
// (JIT provisioning) and issues a token pair.
func (p *OIDCAuthProvider) ExchangeCode(ctx context.Context, req OIDCCallbackRequest) (*TokenPair, error) {
	cfg, id, err := p.verifyCallback(ctx, req)
	if err != nil {
		return nil, err
	}

	user, err := p.findOrProvisionUser(ctx, cfg, id)
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

// LinkIdentity completes an OIDC flow started by an authenticated local user
// and links the verified identity to their account. The account becomes
// SSO-only: its password is removed, since the identity provider now owns
// authentication. The caller clears its two-factor state.
func (p *OIDCAuthProvider) LinkIdentity(ctx context.Context, req OIDCCallbackRequest, userID uuid.UUID) (*db.User, error) {
	cfg, id, err := p.verifyCallback(ctx, req)
	if err != nil {
		return nil, err
	}

	_, err = p.userRepo.GetByOIDC(ctx, cfg.ID.String(), id.sub)
	if err == nil {
		return nil, ErrOIDCIdentityInUse
	}
	if !isNotFound(err) {
		return nil, fmt.Errorf("auth: looking up OIDC user: %w", err)
	}

	user, err := p.userRepo.GetByID(ctx, userID)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("auth: fetching user to link: %w", err)
	}

	user.OIDCProvider = cfg.ID.String()
	user.OIDCSub = id.sub
	user.Password = ""
	if role, ok := mappedRole(cfg, id.groups); ok {
		user.Role = role
	}
	if err := p.userRepo.Update(ctx, user); err != nil {
		return nil, fmt.Errorf("auth: linking OIDC identity: %w", err)
	}
	return user, nil
}

// verifyCallback verifies the state and PKCE verifier, exchanges the code,
// verifies the ID token and extracts the identity. When the provider restricts
// sign-in to allowed groups it also enforces that, before any account is
// touched.
func (p *OIDCAuthProvider) verifyCallback(ctx context.Context, req OIDCCallbackRequest) (*db.OIDCProvider, *oidcIdentity, error) {
	if req.State != req.SessionState {
		return nil, nil, ErrOIDCStateMismatch
	}

	if req.CodeVerifier == "" {
		return nil, nil, ErrOIDCCodeVerifierMissing
	}

	providerID, err := uuid.Parse(req.ProviderID)
	if err != nil {
		return nil, nil, fmt.Errorf("auth: invalid provider ID %q: %w", req.ProviderID, err)
	}

	cfg, oauth2Cfg, err := p.loadConfig(ctx, providerID, req.CallbackURL)
	if err != nil {
		return nil, nil, err
	}

	// Exchange the authorization code for an OAuth2 token set.
	oauth2Token, err := oauth2Cfg.Exchange(
		ctx,
		req.Code,
		oauth2.VerifierOption(req.CodeVerifier),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("auth: exchanging OIDC code: %w", err)
	}

	// Extract and verify the ID token from the OAuth2 token response.
	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		return nil, nil, fmt.Errorf("auth: OIDC token response missing id_token")
	}

	// Use the discovered provider to verify the ID token signature and claims.
	oidcProvider, err := gooidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, nil, fmt.Errorf("auth: initializing OIDC provider for issuer %q: %w", cfg.Issuer, err)
	}

	verifier := oidcProvider.Verifier(&gooidc.Config{ClientID: cfg.ClientID})

	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, nil, fmt.Errorf("auth: verifying OIDC id_token: %w", err)
	}

	// Extract standard claims from the verified ID token.
	var claims struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, nil, fmt.Errorf("auth: extracting OIDC claims: %w", err)
	}
	var allClaims map[string]any
	if err := idToken.Claims(&allClaims); err != nil {
		return nil, nil, fmt.Errorf("auth: extracting OIDC claims: %w", err)
	}

	// Groups are only needed when the provider restricts or maps by group.
	needGroups := cfg.AllowedGroups != "" || cfg.AdminGroups != ""
	groupsClaim, haveGroups := allClaims[cfg.GroupsClaim]

	// Many providers (e.g. Zitadel, Keycloak) only guarantee sub in the ID
	// token and return email/name/groups via the UserInfo endpoint. Fetch
	// UserInfo as a fallback whenever a needed claim is missing.
	if claims.Email == "" || claims.Name == "" || (needGroups && !haveGroups) {
		userInfo, uiErr := oidcProvider.UserInfo(ctx, oauth2.StaticTokenSource(oauth2Token))
		if uiErr != nil {
			p.logger.Warn("OIDC userinfo fetch failed, proceeding with ID token claims only",
				zap.Error(uiErr))
		} else {
			var uiClaims map[string]any
			if uiErr = userInfo.Claims(&uiClaims); uiErr == nil {
				if claims.Email == "" {
					claims.Email, _ = uiClaims["email"].(string)
				}
				if claims.Name == "" {
					claims.Name, _ = uiClaims["name"].(string)
				}
				if !haveGroups {
					groupsClaim = uiClaims[cfg.GroupsClaim]
				}
			}
		}
	}

	// sub is guaranteed by OIDC spec; email is required to provision an account.
	if claims.Sub == "" {
		return nil, nil, fmt.Errorf("auth: OIDC id_token missing required 'sub' claim")
	}
	if claims.Email == "" {
		return nil, nil, fmt.Errorf("auth: identity provider did not return an email address — ensure the 'email' scope is requested and the provider is configured to include it")
	}
	// Fall back to sub as display name if the provider does not return one.
	if claims.Name == "" {
		claims.Name = claims.Sub
	}

	id := &oidcIdentity{sub: claims.Sub, email: claims.Email, name: claims.Name, groups: claimGroups(groupsClaim)}

	// Fail closed: a missing or empty groups claim matches no allowed group.
	if cfg.AllowedGroups != "" && !inAnyGroup(id.groups, cfg.AllowedGroups) {
		p.logger.Warn("OIDC login denied: user is not in an allowed group",
			zap.String("provider", cfg.Name),
			zap.String("sub", id.sub),
			zap.String("email", id.email),
			zap.String("groups_claim", cfg.GroupsClaim),
			zap.Strings("groups", id.groups),
		)
		return nil, nil, ErrOIDCAccessDenied
	}

	return cfg, id, nil
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
	// A disabled provider is hidden from the login page; refuse it here too so
	// its login URL cannot be used directly.
	if !cfg.Enabled {
		return nil, nil, ErrProviderNotFound
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
//  1. By (oidc_provider, oidc_sub) — returning OIDC user.
//  2. Otherwise JIT-provision a new account, unless an account with the same
//     email already exists: accounts are never linked by email, since a user
//     who controls that address at the IdP would take over the account. The
//     owner links it from their profile instead (LinkIdentity).
//
// Email and display name are synced from the IdP on every login, and so is the
// role when the provider maps admin_groups.
func (p *OIDCAuthProvider) findOrProvisionUser(ctx context.Context, cfg *db.OIDCProvider, id *oidcIdentity) (*db.User, error) {
	role, mapped := mappedRole(cfg, id.groups)

	user, err := p.userRepo.GetByOIDC(ctx, cfg.ID.String(), id.sub)
	if err != nil && !isNotFound(err) {
		return nil, fmt.Errorf("auth: looking up OIDC user: %w", err)
	}

	if isNotFound(err) {
		_, err = p.userRepo.GetByEmail(ctx, id.email)
		if err == nil {
			return nil, ErrOIDCAccountExists
		}
		if !isNotFound(err) {
			return nil, fmt.Errorf("auth: looking up user by email: %w", err)
		}

		if !mapped {
			role = "user"
		}
		newUser := &db.User{
			Email:        id.email,
			DisplayName:  id.name,
			Role:         role,
			IsActive:     true,
			OIDCProvider: cfg.ID.String(),
			OIDCSub:      id.sub,
		}
		if err := p.userRepo.Create(ctx, newUser); err != nil {
			return nil, fmt.Errorf("auth: provisioning OIDC user: %w", err)
		}
		return newUser, nil
	}

	// Update email and display name in case they changed at the IdP.
	user.Email = id.email
	user.DisplayName = id.name
	if mapped {
		user.Role = role
	}
	if updateErr := p.userRepo.Update(ctx, user); updateErr != nil {
		p.logger.Warn("failed to update user profile from OIDC claims",
			zap.String("user_id", user.ID.String()),
			zap.Error(updateErr),
		)
	}
	return user, nil
}

// mappedRole returns the role derived from the provider's admin_groups, and
// false when the provider does not map roles.
func mappedRole(cfg *db.OIDCProvider, groups []string) (string, bool) {
	if cfg.AdminGroups == "" {
		return "", false
	}
	if inAnyGroup(groups, cfg.AdminGroups) {
		return "admin", true
	}
	return "user", true
}

// claimGroups normalises a groups claim into a list of group names. Providers
// disagree on its shape: an array of names (Keycloak, Authentik, Authelia,
// Entra), a single name as a string, or an object whose keys are the names
// (Zitadel project roles). Anything else yields no groups.
func claimGroups(v any) []string {
	switch c := v.(type) {
	case string:
		return []string{c}
	case []any:
		var groups []string
		for _, g := range c {
			if s, ok := g.(string); ok {
				groups = append(groups, s)
			}
		}
		return groups
	case map[string]any:
		groups := make([]string, 0, len(c))
		for g := range c {
			groups = append(groups, g)
		}
		return groups
	default:
		return nil
	}
}

// inAnyGroup reports whether groups contains one of the names in configured,
// a comma-separated list.
func inAnyGroup(groups []string, configured string) bool {
	for _, want := range strings.Split(configured, ",") {
		if want = strings.TrimSpace(want); want != "" && slices.Contains(groups, want) {
			return true
		}
	}
	return false
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
