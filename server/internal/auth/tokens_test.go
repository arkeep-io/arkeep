package auth

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// TestRefreshToken_RejectsExpired guards against a refresh path that skips the
// expiry check: the OIDC provider used to carry its own copy of the refresh
// logic without it, so a stored token past ExpiresAt would have been rotated.
func TestRefreshToken_RejectsExpired(t *testing.T) {
	if err := db.InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("db.InitEncryption: %v", err)
	}
	gdb, err := db.New(db.Config{Driver: "sqlite", DSN: ":memory:", Logger: zap.NewNop(), LogLevel: gormlogger.Silent})
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	users := repositories.NewUserRepository(gdb)
	tokens := repositories.NewRefreshTokenRepository(gdb)
	jwtMgr, err := NewJWTManagerGenerated("arkeep-test")
	if err != nil {
		t.Fatalf("jwt: %v", err)
	}
	ctx := context.Background()

	user := &db.User{Email: "u@example.com", DisplayName: "U", Role: "user", IsActive: true}
	if err := users.Create(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	providers := map[string]interface {
		RefreshToken(context.Context, string) (*TokenPair, error)
	}{
		"local": NewLocalAuthProvider(users, tokens, jwtMgr, zap.NewNop()),
		"oidc":  NewOIDCAuthProvider(repositories.NewOIDCProviderRepository(gdb), users, tokens, jwtMgr, zap.NewNop()),
	}
	for name, p := range providers {
		t.Run(name, func(t *testing.T) {
			raw, err := generateRefreshToken()
			if err != nil {
				t.Fatalf("generateRefreshToken: %v", err)
			}
			if err := tokens.Create(ctx, &db.RefreshToken{
				UserID:    user.ID,
				TokenHash: hashRefreshToken(raw),
				ExpiresAt: time.Now().Add(-time.Minute),
			}); err != nil {
				t.Fatalf("create refresh token: %v", err)
			}

			if _, err := p.RefreshToken(ctx, raw); !errors.Is(err, ErrTokenExpired) {
				t.Errorf("RefreshToken(expired) error = %v, want ErrTokenExpired", err)
			}
		})
	}
}
