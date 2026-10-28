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

// TestRefreshToken_OIDCSessionDeadline checks that refreshing never extends an
// OIDC session: the deadline is copied to the rotated token, caps its expiry,
// and once passed the refresh is refused even though the token itself has
// not expired. A local session keeps no deadline.
func TestRefreshToken_OIDCSessionDeadline(t *testing.T) {
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
	issuer := &tokenIssuer{userRepo: users, tokenRepo: tokens, jwtManager: jwtMgr}
	ctx := context.Background()

	user := &db.User{Email: "u@example.com", DisplayName: "U", Role: "user", IsActive: true}
	if err := users.Create(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// storedToken returns the refresh token row behind a raw token.
	storedToken := func(t *testing.T, raw string) *db.RefreshToken {
		t.Helper()
		got, err := tokens.GetByHash(ctx, hashRefreshToken(raw))
		if err != nil {
			t.Fatalf("GetByHash: %v", err)
		}
		return got
	}

	t.Run("rotation keeps the deadline", func(t *testing.T) {
		deadline := time.Now().Add(2 * time.Hour).Truncate(time.Second)
		pair, err := issuer.issueTokenPair(ctx, user.ID, user.Email, user.Role, &deadline)
		if err != nil {
			t.Fatalf("issueTokenPair: %v", err)
		}
		if !pair.RefreshTokenExpiresAt.Equal(deadline) {
			t.Errorf("RefreshTokenExpiresAt = %v, want the session deadline %v", pair.RefreshTokenExpiresAt, deadline)
		}

		rotated, err := issuer.RefreshToken(ctx, pair.RefreshToken)
		if err != nil {
			t.Fatalf("RefreshToken: %v", err)
		}
		got := storedToken(t, rotated.RefreshToken)
		if got.SessionExpiresAt == nil || !got.SessionExpiresAt.Equal(deadline) {
			t.Errorf("rotated SessionExpiresAt = %v, want %v", got.SessionExpiresAt, deadline)
		}
		if !got.ExpiresAt.Equal(deadline) {
			t.Errorf("rotated ExpiresAt = %v, want %v", got.ExpiresAt, deadline)
		}
	})

	t.Run("refused past the deadline", func(t *testing.T) {
		raw, err := generateRefreshToken()
		if err != nil {
			t.Fatalf("generateRefreshToken: %v", err)
		}
		deadline := time.Now().Add(-time.Minute)
		if err := tokens.Create(ctx, &db.RefreshToken{
			UserID:           user.ID,
			TokenHash:        hashRefreshToken(raw),
			ExpiresAt:        time.Now().Add(time.Hour),
			SessionExpiresAt: &deadline,
		}); err != nil {
			t.Fatalf("create refresh token: %v", err)
		}
		if _, err := issuer.RefreshToken(ctx, raw); !errors.Is(err, ErrTokenExpired) {
			t.Errorf("RefreshToken(past deadline) error = %v, want ErrTokenExpired", err)
		}
	})

	t.Run("local session has no deadline", func(t *testing.T) {
		pair, err := issuer.issueTokenPair(ctx, user.ID, user.Email, user.Role, nil)
		if err != nil {
			t.Fatalf("issueTokenPair: %v", err)
		}
		got := storedToken(t, pair.RefreshToken)
		if got.SessionExpiresAt != nil {
			t.Errorf("SessionExpiresAt = %v, want nil", *got.SessionExpiresAt)
		}
		if want := time.Now().Add(refreshTokenDuration - time.Minute); got.ExpiresAt.Before(want) {
			t.Errorf("ExpiresAt = %v, want about %v from now", got.ExpiresAt, refreshTokenDuration)
		}
	})
}
