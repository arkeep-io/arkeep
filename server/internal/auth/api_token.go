package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// API tokens (ADR 0027) are long-lived, read-only credentials a user creates
// for a program that reads the API on their behalf.
const (
	// APITokenPrefix starts every API token, so that it is recognised by the
	// middleware, by secret scanners, and by a user who finds one.
	APITokenPrefix = "ark_"

	// APITokenScopeRead is the only scope so far: GET requests with the
	// rights of the "user" role.
	APITokenScopeRead = "read"

	// apiTokenRandomBytes is the entropy of a token: 256 bits.
	apiTokenRandomBytes = 32

	// apiTokenHintLen is how much of the raw token is kept as its hint:
	// the prefix and 4 random characters.
	apiTokenHintLen = len(APITokenPrefix) + 4

	// apiTokenTouchInterval limits how often last_used_at is written for a
	// token in use: a program polling every few seconds would otherwise
	// write on every request.
	apiTokenTouchInterval = time.Minute
)

// ErrAPITokenInvalid is returned for an API token that does not exist, was
// revoked, has expired or belongs to a disabled user. The cases are not told
// apart to the client.
var ErrAPITokenInvalid = errors.New("auth: API token invalid")

// IsAPIToken reports whether a bearer credential is an API token rather than
// a JWT access token. A JWT never starts with the prefix: it starts with the
// base64 of its JSON header, "eyJ".
func IsAPIToken(raw string) bool {
	return strings.HasPrefix(raw, APITokenPrefix)
}

// GenerateAPIToken returns a new raw token, its SHA-256 hex hash, and its
// hint.
func GenerateAPIToken() (raw, hash, hint string, err error) {
	b := make([]byte, apiTokenRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", "", err
	}
	raw = APITokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return raw, HashAPIToken(raw), raw[:apiTokenHintLen], nil
}

// HashAPIToken returns the SHA-256 hex hash under which a token is stored.
// The token has 256 bits of entropy, so a fast hash is enough: there is
// nothing to brute-force, unlike a password.
func HashAPIToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// APITokenValidator authenticates requests made with an API token.
type APITokenValidator struct {
	tokens repositories.APITokenRepository
	users  repositories.UserRepository
	logger *zap.Logger
	now    func() time.Time

	// touched remembers when last_used_at was last written for each token.
	mu      sync.Mutex
	touched map[string]time.Time
}

// NewAPITokenValidator returns a validator reading tokens and users from the
// given repositories.
func NewAPITokenValidator(tokens repositories.APITokenRepository, users repositories.UserRepository, logger *zap.Logger) *APITokenValidator {
	return &APITokenValidator{
		tokens:  tokens,
		users:   users,
		logger:  logger,
		now:     time.Now,
		touched: make(map[string]time.Time),
	}
}

// ValidateAPIToken returns the claims of the request made with raw: the
// owner's identity, with the "user" role whatever the owner's role, since an
// API token is read-only. It records the token's use, at most once a minute.
func (v *APITokenValidator) ValidateAPIToken(ctx context.Context, raw string) (*Claims, *db.APIToken, error) {
	if !IsAPIToken(raw) {
		return nil, nil, ErrAPITokenInvalid
	}
	token, err := v.tokens.GetByHash(ctx, HashAPIToken(raw))
	if err != nil {
		if !errors.Is(err, repositories.ErrNotFound) {
			v.logger.Error("api token lookup failed", zap.Error(err))
		}
		return nil, nil, ErrAPITokenInvalid
	}
	now := v.now()
	if token.ExpiresAt != nil && !now.Before(*token.ExpiresAt) {
		return nil, nil, ErrAPITokenInvalid
	}
	if token.Scope != APITokenScopeRead {
		return nil, nil, ErrAPITokenInvalid
	}
	user, err := v.users.GetByID(ctx, token.UserID)
	if err != nil {
		if !errors.Is(err, repositories.ErrNotFound) {
			v.logger.Error("api token owner lookup failed", zap.Error(err))
		}
		return nil, nil, ErrAPITokenInvalid
	}
	if !user.IsActive {
		return nil, nil, ErrAPITokenInvalid
	}

	v.touch(ctx, token, now)

	claims := &Claims{
		UserID: user.ID.String(),
		Email:  user.Email,
		Role:   "user",
	}
	// The JWT ID identifies the credential in logs and audit entries; it is
	// never looked up in the denylist, which only holds JWT IDs.
	claims.ID = "api-token:" + token.ID.String()
	return claims, token, nil
}

// touch writes the token's last use, unless it was written less than
// apiTokenTouchInterval ago. A failure is logged and does not fail the
// request.
func (v *APITokenValidator) touch(ctx context.Context, token *db.APIToken, now time.Time) {
	key := token.ID.String()
	v.mu.Lock()
	last, ok := v.touched[key]
	if ok && now.Sub(last) < apiTokenTouchInterval {
		v.mu.Unlock()
		return
	}
	v.touched[key] = now
	// Forget tokens not used for a while, so the map does not keep revoked
	// ones forever.
	for k, t := range v.touched {
		if now.Sub(t) > 10*apiTokenTouchInterval {
			delete(v.touched, k)
		}
	}
	v.mu.Unlock()

	if err := v.tokens.TouchLastUsed(ctx, token.ID, now); err != nil {
		v.logger.Warn("cannot record api token use", zap.Error(err))
	}
}
