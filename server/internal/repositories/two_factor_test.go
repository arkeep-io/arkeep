package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"gorm.io/gorm"
)

// newTwoFactorUser inserts a minimal local user to satisfy the FK on both new
// tables and returns it.
func newTwoFactorUser(t *testing.T, gdb *gorm.DB) *db.User {
	t.Helper()
	u := &db.User{Email: "tfa@test.local", DisplayName: "TFA", Role: "user", IsActive: true}
	if err := NewUserRepository(gdb).Create(context.Background(), u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func TestTwoFactorChallengeRepository(t *testing.T) {
	gdb := newTestDB(t)
	repo := NewTwoFactorChallengeRepository(gdb)
	ctx := context.Background()
	user := newTwoFactorUser(t, gdb)

	c := &db.TwoFactorChallenge{
		UserID:    user.ID,
		TokenHash: "hash-a",
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Run("GetUnusedByHash returns the row", func(t *testing.T) {
		got, err := repo.GetUnusedByHash(ctx, "hash-a")
		if err != nil {
			t.Fatalf("GetUnusedByHash: %v", err)
		}
		if got.ID != c.ID || got.Attempts != 0 {
			t.Errorf("got id=%s attempts=%d, want id=%s attempts=0", got.ID, got.Attempts, c.ID)
		}
	})

	t.Run("IncrementAttempts increments", func(t *testing.T) {
		if err := repo.IncrementAttempts(ctx, c.ID); err != nil {
			t.Fatalf("IncrementAttempts: %v", err)
		}
		got, err := repo.GetUnusedByHash(ctx, "hash-a")
		if err != nil {
			t.Fatalf("GetUnusedByHash: %v", err)
		}
		if got.Attempts != 1 {
			t.Errorf("attempts = %d, want 1", got.Attempts)
		}
	})

	t.Run("MarkUsed makes it unfindable", func(t *testing.T) {
		if err := repo.MarkUsed(ctx, c.ID); err != nil {
			t.Fatalf("MarkUsed: %v", err)
		}
		if _, err := repo.GetUnusedByHash(ctx, "hash-a"); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("DeleteByUserID removes outstanding challenges", func(t *testing.T) {
		fresh := &db.TwoFactorChallenge{UserID: user.ID, TokenHash: "hash-b", ExpiresAt: time.Now().Add(time.Minute)}
		if err := repo.Create(ctx, fresh); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := repo.DeleteByUserID(ctx, user.ID); err != nil {
			t.Fatalf("DeleteByUserID: %v", err)
		}
		if _, err := repo.GetUnusedByHash(ctx, "hash-b"); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestRecoveryCodeRepository(t *testing.T) {
	gdb := newTestDB(t)
	repo := NewRecoveryCodeRepository(gdb)
	ctx := context.Background()
	user := newTwoFactorUser(t, gdb)

	codes := []db.RecoveryCode{
		{UserID: user.ID, CodeHash: "rc-1"},
		{UserID: user.ID, CodeHash: "rc-2"},
	}
	if err := repo.CreateBatch(ctx, codes); err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}

	n, err := repo.CountUnused(ctx, user.ID)
	if err != nil {
		t.Fatalf("CountUnused: %v", err)
	}
	if n != 2 {
		t.Fatalf("CountUnused = %d, want 2", n)
	}

	got, err := repo.GetUnusedByHash(ctx, "rc-1")
	if err != nil {
		t.Fatalf("GetUnusedByHash: %v", err)
	}
	if err := repo.MarkUsed(ctx, got.ID); err != nil {
		t.Fatalf("MarkUsed: %v", err)
	}

	if _, err := repo.GetUnusedByHash(ctx, "rc-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("reused code err = %v, want ErrNotFound", err)
	}

	n, err = repo.CountUnused(ctx, user.ID)
	if err != nil {
		t.Fatalf("CountUnused: %v", err)
	}
	if n != 1 {
		t.Errorf("CountUnused after use = %d, want 1", n)
	}
}

// TestRefreshTokenRevocationIsEnforced is the regression test for revoked
// refresh tokens remaining redeemable: GetByHash used to ignore revoked_at.
func TestRefreshTokenRevocationIsEnforced(t *testing.T) {
	gdb := newTestDB(t)
	repo := NewRefreshTokenRepository(gdb)
	ctx := context.Background()
	user := newTwoFactorUser(t, gdb)

	keep := &db.RefreshToken{UserID: user.ID, TokenHash: "keep", ExpiresAt: time.Now().Add(time.Hour)}
	drop := &db.RefreshToken{UserID: user.ID, TokenHash: "drop", ExpiresAt: time.Now().Add(time.Hour)}
	for _, tok := range []*db.RefreshToken{keep, drop} {
		if err := repo.Create(ctx, tok); err != nil {
			t.Fatalf("Create %s: %v", tok.TokenHash, err)
		}
	}

	t.Run("revoked token is not redeemable", func(t *testing.T) {
		if err := repo.Revoke(ctx, drop.ID); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		if _, err := repo.GetByHash(ctx, "drop"); !errors.Is(err, ErrNotFound) {
			t.Errorf("GetByHash(revoked) err = %v, want ErrNotFound", err)
		}
	})

	t.Run("RevokeAllForUserExcept spares the current session", func(t *testing.T) {
		other := &db.RefreshToken{UserID: user.ID, TokenHash: "other", ExpiresAt: time.Now().Add(time.Hour)}
		if err := repo.Create(ctx, other); err != nil {
			t.Fatalf("Create other: %v", err)
		}

		if err := repo.RevokeAllForUserExcept(ctx, user.ID, "keep"); err != nil {
			t.Fatalf("RevokeAllForUserExcept: %v", err)
		}

		if _, err := repo.GetByHash(ctx, "keep"); err != nil {
			t.Errorf("kept token err = %v, want nil", err)
		}
		if _, err := repo.GetByHash(ctx, "other"); !errors.Is(err, ErrNotFound) {
			t.Errorf("other token err = %v, want ErrNotFound", err)
		}
	})
}
