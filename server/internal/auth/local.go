package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repository"
)

const (
	// refreshTokenDuration defines how long a refresh token remains valid.
	refreshTokenDuration = 7 * 24 * time.Hour

	// argon2Time is the number of iterations (time cost) for Argon2id.
	// OWASP minimum recommendation is 1; 2 provides a better security margin.
	argon2Time = 2

	// argon2Memory is the memory cost in KiB for Argon2id (64 MiB).
	argon2Memory = 64 * 1024

	// argon2Threads is the parallelism factor for Argon2id.
	argon2Threads = 2

	// argon2KeyLen is the output hash length in bytes.
	argon2KeyLen = 32

	// argon2SaltLen is the random salt length in bytes.
	argon2SaltLen = 16

	// refreshTokenBytes is the length of the random refresh token before encoding.
	refreshTokenBytes = 32
)

// LocalAuthProvider authenticates users via email/password stored in the
// database. Passwords are hashed with Argon2id and stored as EncryptedString
// (AES-256-GCM at rest). Refresh tokens are stored as SHA-256 hashes so the
// raw token is never persisted.
type LocalAuthProvider struct {
	userRepo   repository.UserRepository
	tokenRepo  repository.RefreshTokenRepository
	jwtManager *JWTManager
}

// NewLocalAuthProvider creates a LocalAuthProvider with the given dependencies.
func NewLocalAuthProvider(
	userRepo repository.UserRepository,
	tokenRepo repository.RefreshTokenRepository,
	jwtManager *JWTManager,
) *LocalAuthProvider {
	return &LocalAuthProvider{
		userRepo:   userRepo,
		tokenRepo:  tokenRepo,
		jwtManager: jwtManager,
	}
}

// ProviderType implements AuthProvider.
func (p *LocalAuthProvider) ProviderType() string {
	return "local"
}

// Login validates email/password and returns a token pair on success.
// The password is verified against the Argon2id hash stored in the database
// and encrypted at rest via EncryptedString.
func (p *LocalAuthProvider) Login(ctx context.Context, req LoginRequest) (*TokenPair, error) {
	user, err := p.userRepo.GetByEmail(ctx, req.Email)
	if err != nil {
		if isNotFound(err) {
			// Return ErrInvalidCredentials instead of ErrUserNotFound to avoid
			// leaking whether the email address is registered (user enumeration).
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("auth: fetching user by email: %w", err)
	}

	if !user.IsActive {
		return nil, ErrUserDisabled
	}

	if !verifyPassword(req.Password, string(user.Password)) {
		return nil, ErrInvalidCredentials
	}

	return p.issueTokenPair(ctx, user.ID, user.Email, user.Role)
}

// RefreshToken validates a refresh token, rotates it, and issues a new token pair.
// The old token is deleted before issuing the new one — if the issue fails the
// user must log in again. This prevents replay attacks even on partial failures.
func (p *LocalAuthProvider) RefreshToken(ctx context.Context, rawToken string) (*TokenPair, error) {
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
func (p *LocalAuthProvider) Logout(ctx context.Context, rawToken string) error {
	tokenHash := hashRefreshToken(rawToken)

	if err := p.tokenRepo.DeleteByHash(ctx, tokenHash); err != nil && !isNotFound(err) {
		return fmt.Errorf("auth: revoking refresh token on logout: %w", err)
	}

	return nil
}

// issueTokenPair generates a new access token and refresh token, persists the
// refresh token hash, and returns both as a TokenPair.
func (p *LocalAuthProvider) issueTokenPair(ctx context.Context, userID uuid.UUID, email, role string) (*TokenPair, error) {
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
		RefreshToken:          rawRefresh,
		RefreshTokenExpiresAt: expiresAt,
	}, nil
}

// HashPassword returns an Argon2id hash of the given plaintext password.
// Exported so the user registration handler can hash passwords without
// depending on the full auth provider.
//
// Format: saltHex:hashHex
func HashPassword(password string) (string, error) {
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generating password salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

	return hex.EncodeToString(salt) + ":" + hex.EncodeToString(hash), nil
}

// verifyPassword checks a plaintext password against a stored Argon2id hash.
// Returns false if the hash format is invalid rather than propagating an error,
// since an invalid hash means authentication must fail.
func verifyPassword(password, stored string) bool {
	saltHex, hashHex, ok := splitHash(stored)
	if !ok {
		return false
	}

	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return false
	}

	expectedHash, err := hex.DecodeString(hashHex)
	if err != nil {
		return false
	}

	actual := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Threads, uint32(len(expectedHash)))

	return constantTimeEqual(actual, expectedHash)
}

// hashRefreshToken returns the SHA-256 hex digest of a raw refresh token.
// Only the hash is stored in the database — the raw token lives only in the cookie.
func hashRefreshToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// generateRefreshToken returns a cryptographically random hex-encoded token string.
func generateRefreshToken() (string, error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// splitHash splits a "saltHex:hashHex" string into its two components.
func splitHash(s string) (salt, hash string, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

// constantTimeEqual compares two byte slices in constant time to prevent
// timing-based side-channel attacks.
func constantTimeEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// isNotFound checks for the repository ErrNotFound sentinel error.
func isNotFound(err error) bool {
	return err != nil && err.Error() == repository.ErrNotFound.Error()
}