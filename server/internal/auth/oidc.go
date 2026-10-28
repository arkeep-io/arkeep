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
	tokenIssuer
	providerRepo repositories.OIDCProviderRepository
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
		tokenIssuer:  tokenIssuer{userRepo: userRepo, tokenRepo: tokenRepo, jwtManager: jwtManager},
		providerRepo: providerRepo,
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
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified any    `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("auth: extracting OIDC claims: %w", err)
	}
	emailVerified := claimIsTrue(claims.EmailVerified)

	// The groups claim is named by the admin and its value may be a list, a
	// string or an object, so it is read from the raw claims.
	needGroups := cfg.AllowedGroups != "" || cfg.AdminGroups != ""
	var groups []string
	groupsFound := false
	if needGroups {
		var rawClaims map[string]any
		if err := idToken.Claims(&rawClaims); err != nil {
			return nil, fmt.Errorf("auth: extracting OIDC claims: %w", err)
		}
		groups, groupsFound = groupsFromClaim(rawClaims[cfg.GroupsClaim])
	}

	// Many providers (e.g. Zitadel, Keycloak) only guarantee sub in the ID
	// token and return email/name/groups via the UserInfo endpoint. Fetch
	// UserInfo as a fallback whenever one of them is missing.
	if claims.Email == "" || claims.Name == "" || (needGroups && !groupsFound) {
		userInfo, uiErr := oidcProvider.UserInfo(ctx, oauth2.StaticTokenSource(oauth2Token))
		if uiErr != nil {
			p.logger.Warn("OIDC userinfo fetch failed, proceeding with ID token claims only",
				zap.Error(uiErr))
		} else {
			var uiClaims struct {
				Sub           string `json:"sub"`
				Email         string `json:"email"`
				EmailVerified any    `json:"email_verified"`
				Name          string `json:"name"`
			}
			// UserInfo is only trusted for the subject the verified ID token
			// names (OIDC Core 5.3.2).
			if uiErr = userInfo.Claims(&uiClaims); uiErr == nil && uiClaims.Sub == claims.Sub {
				if claims.Email == "" {
					claims.Email = uiClaims.Email
					emailVerified = claimIsTrue(uiClaims.EmailVerified)
				}
				if claims.Name == "" {
					claims.Name = uiClaims.Name
				}
				if needGroups && !groupsFound {
					var uiRaw map[string]any
					if userInfo.Claims(&uiRaw) == nil {
						groups, groupsFound = groupsFromClaim(uiRaw[cfg.GroupsClaim])
					}
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

	// The group check runs before any account is provisioned or linked, so a
	// user outside the allowed groups never gets one.
	if needGroups && !groupsFound {
		p.logger.Warn("OIDC groups claim not present, check the client scope or claim mapper on the identity provider",
			zap.String("provider", cfg.ID.String()),
			zap.String("claim", cfg.GroupsClaim),
		)
	}
	allowed, role := decideGroups(cfg.AllowedGroups, cfg.AdminGroups, groups)
	if !allowed {
		p.logger.Warn("OIDC login denied: user is not a member of an allowed group",
			zap.String("provider", cfg.ID.String()),
			zap.String("sub", claims.Sub),
			zap.Strings("groups", groups),
		)
		p.revokeSessions(ctx, cfg.ID.String(), claims.Sub)
		return nil, ErrOIDCGroupDenied
	}

	user, err := p.findOrProvisionUser(ctx, cfg, claims.Sub, claims.Email, emailVerified, claims.Name, role)
	if err != nil {
		return nil, err
	}

	if !user.IsActive {
		return nil, ErrUserDisabled
	}

	// The session must pass through the IdP again after SessionMaxHours, so
	// that the groups are re-checked.
	sessionExpiresAt := time.Now().Add(time.Duration(cfg.SessionMaxHours) * time.Hour)

	// The IdP proved the first factor only: an account with two-factor
	// authentication still owes its second one (SEC-06). As for a local login,
	// LastLoginAt is not stamped until the challenge is completed.
	if user.TwoFactorEnabled {
		return nil, &TwoFactorRequiredError{UserID: user.ID, SessionExpiresAt: &sessionExpiresAt}
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

	return p.issueTokenPair(ctx, user.ID, user.Email, user.Role, &sessionExpiresAt)
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
	// A disabled provider is treated as absent: disabling one (e.g. because
	// the IdP was compromised) must stop logins through it, not only hide
	// its button on the login page.
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
//  1. By (oidc_provider, oidc_sub) — returning OIDC user, fast path.
//  2. By email — account linking: an existing account with the same email is
//     adopted for this OIDC provider so that no duplicate is created, but only
//     when canLinkByEmail allows it (verified email, not an admin, not
//     already linked to another provider).
//  3. Neither found — JIT-provision a new account with role "user", or the
//     role granted by the provider's admin groups.
//
// Email and display name are synced from the IdP on every login. role is the
// role granted by the provider's admin groups, or "" to leave it unchanged.
func (p *OIDCAuthProvider) findOrProvisionUser(ctx context.Context, cfg *db.OIDCProvider, sub, email string, emailVerified bool, displayName, role string) (*db.User, error) {
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
			newRole := "user"
			if role != "" {
				newRole = role
			}
			newUser := &db.User{
				Email:        email,
				DisplayName:  displayName,
				Role:         newRole,
				IsActive:     true,
				OIDCProvider: cfg.ID.String(),
				OIDCSub:      sub,
			}
			if err := p.userRepo.Create(ctx, newUser); err != nil {
				return nil, fmt.Errorf("auth: provisioning OIDC user: %w", err)
			}
			return newUser, nil
		}

		// Existing account found by email — link it to this OIDC provider
		// only if that cannot hand the account to someone else.
		if err := canLinkByEmail(user, cfg.ID.String(), emailVerified); err != nil {
			p.logger.Warn("refused to link existing account to OIDC provider",
				zap.String("user_id", user.ID.String()),
				zap.String("provider", cfg.ID.String()),
				zap.Error(err),
			)
			return nil, err
		}
		p.logger.Info("linking existing account to OIDC provider",
			zap.String("user_id", user.ID.String()),
			zap.String("provider", cfg.ID.String()),
		)
		user.OIDCProvider = cfg.ID.String()
		user.OIDCSub = sub
	}

	roleChanged := false
	if role != "" && role != user.Role {
		if roleChanged, err = p.canChangeRole(ctx, user); err != nil {
			return nil, err
		}
		if roleChanged {
			p.logger.Info("changing role from OIDC admin groups",
				zap.String("user_id", user.ID.String()),
				zap.String("from", user.Role),
				zap.String("to", role),
			)
			user.Role = role
		}
	}

	// Update email and display name in case they changed at the IdP.
	user.Email = email
	user.DisplayName = displayName
	if updateErr := p.userRepo.Update(ctx, user); updateErr != nil {
		// The tokens about to be issued carry user.Role: a role change that
		// was not saved must not reach them.
		if roleChanged {
			return nil, fmt.Errorf("auth: saving role from OIDC admin groups: %w", updateErr)
		}
		p.logger.Warn("failed to update user profile from OIDC claims",
			zap.String("user_id", user.ID.String()),
			zap.Error(updateErr),
		)
	}
	return user, nil
}

// canChangeRole reports whether the role of user may be changed by the
// provider's admin groups. Demoting the last active admin is refused, so a
// change at the IdP cannot leave Arkeep without an administrator.
func (p *OIDCAuthProvider) canChangeRole(ctx context.Context, user *db.User) (bool, error) {
	if user.Role != "admin" || !user.IsActive {
		return true, nil
	}
	n, err := p.userRepo.CountActiveAdmins(ctx)
	if err != nil {
		return false, fmt.Errorf("auth: counting active admins: %w", err)
	}
	if n <= 1 {
		p.logger.Warn("not demoting the last active admin despite OIDC admin groups",
			zap.String("user_id", user.ID.String()),
		)
		return false, nil
	}
	return true, nil
}

// revokeSessions ends every session of the account linked to this OIDC
// identity, so a user removed from the allowed groups also loses the sessions
// open in other browsers. Only the account already linked to (provider, sub)
// is touched: matching by email would let a denied IdP account end someone
// else's sessions. Failures are logged, the login is denied regardless.
func (p *OIDCAuthProvider) revokeSessions(ctx context.Context, providerID, sub string) {
	user, err := p.userRepo.GetByOIDC(ctx, providerID, sub)
	if err != nil {
		if !isNotFound(err) {
			p.logger.Warn("failed to look up OIDC user to revoke sessions", zap.Error(err))
		}
		return
	}
	if err := p.tokenRepo.RevokeAllForUser(ctx, user.ID); err != nil {
		p.logger.Warn("failed to revoke sessions of denied OIDC user",
			zap.String("user_id", user.ID.String()),
			zap.Error(err),
		)
	}
}

// decideGroups applies a provider's group settings to the groups a user
// holds. Members of an admin group are always allowed. allowed is false when
// allowedGroups is set and the user is in none of them nor in an admin group.
// role is "admin" or "user" when adminGroups is set, "" otherwise.
func decideGroups(allowedGroups, adminGroups string, userGroups []string) (allowed bool, role string) {
	allowedList := SplitGroups(allowedGroups)
	adminList := SplitGroups(adminGroups)

	isAdmin := hasAnyGroup(userGroups, adminList)
	allowed = len(allowedList) == 0 || isAdmin || hasAnyGroup(userGroups, allowedList)

	if len(adminList) > 0 {
		role = "user"
		if isAdmin {
			role = "admin"
		}
	}
	return allowed, role
}

// SplitGroups splits a comma-separated list of group names, trimming spaces
// around each name and dropping empty entries.
func SplitGroups(s string) []string {
	var groups []string
	for _, g := range strings.Split(s, ",") {
		if g = strings.TrimSpace(g); g != "" {
			groups = append(groups, g)
		}
	}
	return groups
}

// hasAnyGroup reports whether userGroups contains at least one of want.
// Names are compared exactly.
func hasAnyGroup(userGroups, want []string) bool {
	for _, w := range want {
		if slices.Contains(userGroups, w) {
			return true
		}
	}
	return false
}

// groupsFromClaim reads the groups claim, which IdPs shape differently: a
// list of names (Keycloak, Authentik), a single name, or an object whose keys
// are the names (Zitadel's project roles). found is false when the claim is
// absent or holds no group.
func groupsFromClaim(v any) (groups []string, found bool) {
	switch c := v.(type) {
	case []any:
		for _, g := range c {
			if s, ok := g.(string); ok && s != "" {
				groups = append(groups, s)
			}
		}
	case string:
		if c != "" {
			groups = []string{c}
		}
	case map[string]any:
		for k := range c {
			groups = append(groups, k)
		}
	}
	return groups, len(groups) > 0
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

// canLinkByEmail decides whether an existing account found by email may be
// linked to an OIDC identity from providerID. The email is the only thing
// tying the two together, so linking is refused whenever the IdP does not
// vouch for it, or when a mistake would hand over an account that matters:
//   - the IdP did not assert email_verified=true (many IdPs let users pick or
//     edit their email, so an unverified claim proves nothing);
//   - the account is an admin (an IdP account must never become an admin
//     through an email match);
//   - the account is already linked to a different provider.
func canLinkByEmail(user *db.User, providerID string, emailVerified bool) error {
	switch {
	case !emailVerified:
		return fmt.Errorf("%w: the identity provider did not verify the email address", ErrOIDCLinkRefused)
	case user.Role == "admin":
		return fmt.Errorf("%w: admin accounts are never linked automatically", ErrOIDCLinkRefused)
	case user.OIDCProvider != "" && user.OIDCProvider != providerID:
		return fmt.Errorf("%w: the account is already linked to another identity provider", ErrOIDCLinkRefused)
	}
	return nil
}

// claimIsTrue interprets a boolean OIDC claim. The spec makes email_verified a
// JSON boolean, but some IdPs send the string "true".
func claimIsTrue(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return strings.EqualFold(b, "true")
	}
	return false
}
