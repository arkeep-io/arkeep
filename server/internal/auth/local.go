package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"golang.org/x/crypto/argon2"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
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
	tokenIssuer
	logger *zap.Logger
}

// NewLocalAuthProvider creates a LocalAuthProvider with the given dependencies.
func NewLocalAuthProvider(
	userRepo repositories.UserRepository,
	tokenRepo repositories.RefreshTokenRepository,
	jwtManager *JWTManager,
	logger *zap.Logger,
) *LocalAuthProvider {
	return &LocalAuthProvider{
		tokenIssuer: tokenIssuer{userRepo: userRepo, tokenRepo: tokenRepo, jwtManager: jwtManager},
		logger:      logger.Named("local_auth"),
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

	// Password is correct, but a second factor is still outstanding. Return
	// before stamping LastLoginAt — a login halted at the first factor is not a
	// successful login. AuthHandler.Login turns this into a challenge.
	if user.TwoFactorEnabled {
		return nil, &TwoFactorRequiredError{UserID: user.ID}
	}

	return p.IssueTokenPair(ctx, user)
}

// IssueTokenPair completes authentication for an already-verified user: it
// stamps LastLoginAt and issues the token pair. Exported so the two-factor
// login handler can finish a login it started via Login.
func (p *LocalAuthProvider) IssueTokenPair(ctx context.Context, user *db.User) (*TokenPair, error) {
	// Update LastLoginAt to track the most recent successful login.
	// Non-fatal: a failure here should not block the login itself.
	now := time.Now()
	user.LastLoginAt = &now
	if err := p.userRepo.Update(ctx, user); err != nil {
		p.logger.Warn("failed to update LastLoginAt on login",
			zap.String("user_id", user.ID.String()),
			zap.Error(err),
		)
	}

	return p.issueTokenPair(ctx, user.ID, user.Email, user.Role)
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

// GenerateResetToken returns a cryptographically random hex-encoded token
// suitable for single-use links such as password reset. It uses the same
// entropy source and length as refresh tokens.
func GenerateResetToken() (string, error) {
	return generateRefreshToken()
}

// HashToken returns the SHA-256 hex digest of an opaque token. Only the hash is
// persisted; the raw token is delivered to the user (cookie or email link).
func HashToken(raw string) string {
	return hashRefreshToken(raw)
}

// VerifyPassword checks a plaintext password against a stored Argon2id hash.
// Exported so handlers outside this package (e.g. the two-factor disable/
// regenerate endpoints) can re-verify the current password without depending
// on the full auth provider.
func VerifyPassword(password, stored string) bool {
	return verifyPassword(password, stored)
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

	// HashPassword always produces argon2KeyLen-byte hashes; anything else is
	// a malformed stored value.
	if len(expectedHash) != argon2KeyLen {
		return false
	}

	actual := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

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
// Uses errors.Is so that wrapped errors (fmt.Errorf("...: %w", ErrNotFound))
// are also handled correctly, unlike a string comparison.
func isNotFound(err error) bool {
	return errors.Is(err, repositories.ErrNotFound)
}
