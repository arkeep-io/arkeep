package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

func TestPasswordChangeChallengeRepository(t *testing.T) {
	gdb := newTestDB(t)
	repo := NewPasswordChangeChallengeRepository(gdb)
	ctx := context.Background()
	user := newTwoFactorUser(t, gdb)

	c := &db.PasswordChangeChallenge{UserID: user.ID, TokenHash: "hash-a", ExpiresAt: time.Now().Add(5 * time.Minute)}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetUnusedByHash(ctx, "hash-a")
	if err != nil || got.ID != c.ID {
		t.Fatalf("GetUnusedByHash = %v, %v; want the challenge", got, err)
	}

	if err := repo.Consume(ctx, c.ID); err != nil {
		t.Fatalf("first Consume: %v", err)
	}
	// A second request with the same token loses the race.
	if err := repo.Consume(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Consume = %v, want ErrNotFound", err)
	}
	if _, err := repo.GetUnusedByHash(ctx, "hash-a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUnusedByHash after Consume = %v, want ErrNotFound", err)
	}

	other := &db.PasswordChangeChallenge{UserID: user.ID, TokenHash: "hash-b", ExpiresAt: time.Now().Add(5 * time.Minute)}
	if err := repo.Create(ctx, other); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.DeleteByUserID(ctx, user.ID); err != nil {
		t.Fatalf("DeleteByUserID: %v", err)
	}
	if _, err := repo.GetUnusedByHash(ctx, "hash-b"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUnusedByHash after DeleteByUserID = %v, want ErrNotFound", err)
	}
}
