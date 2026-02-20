package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	// accessTokenDuration defines how long an access token remains valid.
	// Short-lived by design — refresh tokens handle session continuity.
	accessTokenDuration = 15 * time.Minute

	// rsaKeyBits is the RSA key size used for JWT signing.
	// 2048 bits is the minimum recommended; 4096 for higher security at the
	// cost of slightly slower signing/verification.
	rsaKeyBits = 2048
)

// Claims holds the custom JWT claims embedded in every access token.
// Standard claims (exp, iat, iss) are included via jwt.RegisteredClaims.
type Claims struct {
	jwt.RegisteredClaims

	// UserID is the UUID of the authenticated user.
	UserID string `json:"uid"`

	// Email is included for convenience so the frontend does not need to
	// fetch the user profile just to display the logged-in identity.
	Email string `json:"email"`

	// Role is the user's role at token issuance time.
	// Access tokens are short-lived so role staleness is acceptable.
	Role string `json:"role"`
}

// JWTManager handles RS256 signing and verification of access tokens.
// It holds the RSA key pair in memory after initialization.
type JWTManager struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
	issuer     string
}

// NewJWTManagerFromFiles loads an RSA key pair from PEM files on disk.
// privateKeyPath must point to a PKCS#8 or PKCS#1 PEM-encoded private key.
// publicKeyPath must point to the corresponding PEM-encoded public key.
//
// Use this in production where keys are mounted as secrets (Docker, Kubernetes).
func NewJWTManagerFromFiles(privateKeyPath, publicKeyPath, issuer string) (*JWTManager, error) {
	privBytes, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("auth: reading private key file: %w", err)
	}

	pubBytes, err := os.ReadFile(publicKeyPath)
	if err != nil {
		return nil, fmt.Errorf("auth: reading public key file: %w", err)
	}

	return newJWTManagerFromPEM(privBytes, pubBytes, issuer)
}

// NewJWTManagerGenerated creates a JWTManager with a freshly generated RSA key pair.
// The keys are ephemeral — they are not persisted anywhere. This means all
// existing tokens are invalidated on server restart.
//
// Suitable for development and single-instance deployments where token
// invalidation on restart is acceptable.
func NewJWTManagerGenerated(issuer string) (*JWTManager, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return nil, fmt.Errorf("auth: generating RSA key pair: %w", err)
	}

	return &JWTManager{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
		issuer:     issuer,
	}, nil
}

// newJWTManagerFromPEM parses PEM-encoded RSA key bytes and returns a JWTManager.
func newJWTManagerFromPEM(privatePEM, publicPEM []byte, issuer string) (*JWTManager, error) {
	privBlock, _ := pem.Decode(privatePEM)
	if privBlock == nil {
		return nil, errors.New("auth: failed to decode private key PEM block")
	}

	// Support both PKCS#1 (RSA PRIVATE KEY) and PKCS#8 (PRIVATE KEY) formats.
	var privateKey *rsa.PrivateKey
	switch privBlock.Type {
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(privBlock.Bytes)
		if err != nil {
			return nil, fmt.Errorf("auth: parsing PKCS#1 private key: %w", err)
		}
		privateKey = key
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(privBlock.Bytes)
		if err != nil {
			return nil, fmt.Errorf("auth: parsing PKCS#8 private key: %w", err)
		}
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("auth: PKCS#8 key is not an RSA key")
		}
		privateKey = rsaKey
	default:
		return nil, fmt.Errorf("auth: unsupported private key PEM type: %s", privBlock.Type)
	}

	pubBlock, _ := pem.Decode(publicPEM)
	if pubBlock == nil {
		return nil, errors.New("auth: failed to decode public key PEM block")
	}

	pubInterface, err := x509.ParsePKIXPublicKey(pubBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("auth: parsing public key: %w", err)
	}

	publicKey, ok := pubInterface.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("auth: public key is not an RSA key")
	}

	return &JWTManager{
		privateKey: privateKey,
		publicKey:  publicKey,
		issuer:     issuer,
	}, nil
}

// GenerateAccessToken creates a signed RS256 JWT for the given user.
// The token expires after accessTokenDuration (15 minutes).
func (m *JWTManager) GenerateAccessToken(userID, email, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(accessTokenDuration)),
			// JTI provides a unique identifier for this token instance.
			// Useful if token revocation via a denylist is added in the future.
			ID: uuid.NewString(),
		},
		UserID: userID,
		Email:  email,
		Role:   role,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)

	signed, err := token.SignedString(m.privateKey)
	if err != nil {
		return "", fmt.Errorf("auth: signing access token: %w", err)
	}

	return signed, nil
}

// ValidateAccessToken parses and verifies a JWT string.
// Returns the embedded Claims on success, or a sentinel error on failure.
//
// Callers should use errors.Is(err, auth.ErrTokenExpired) to distinguish
// expired tokens from tampered/malformed ones.
func (m *JWTManager) ValidateAccessToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(
		tokenString,
		&Claims{},
		func(t *jwt.Token) (any, error) {
			// Reject tokens signed with anything other than RS256.
			// This prevents the "alg:none" and HMAC confusion attacks.
			if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("auth: unexpected signing method: %v", t.Header["alg"])
			}
			return m.publicKey, nil
		},
		jwt.WithIssuer(m.issuer),
		jwt.WithExpirationRequired(),
	)

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrTokenInvalid
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrTokenInvalid
	}

	return claims, nil
}

// PublicKeyPEM returns the public key in PEM-encoded PKIX format.
// Useful for exposing a JWKS endpoint or sharing the key with other services.
func (m *JWTManager) PublicKeyPEM() ([]byte, error) {
	pubBytes, err := x509.MarshalPKIXPublicKey(m.publicKey)
	if err != nil {
		return nil, fmt.Errorf("auth: marshaling public key: %w", err)
	}

	return pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubBytes,
	}), nil
}