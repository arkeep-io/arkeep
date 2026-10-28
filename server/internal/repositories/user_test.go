package repositories

import (
	"context"
	"testing"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

func TestUserRepository_CountActiveAdmins(t *testing.T) {
	gdb := newTestDB(t)
	repo := NewUserRepository(gdb)
	ctx := context.Background()

	users := []struct {
		email    string
		role     string
		isActive bool
	}{
		{"admin@test.local", "admin", true},
		{"disabled-admin@test.local", "admin", false},
		{"user@test.local", "user", true},
	}
	for _, u := range users {
		user := &db.User{Email: u.email, DisplayName: u.email, Role: u.role, IsActive: true}
		if err := repo.Create(ctx, user); err != nil {
			t.Fatalf("create %s: %v", u.email, err)
		}
		// IsActive false is the zero value, which GORM replaces with the column
		// default on insert: disable the account with an update instead.
		if !u.isActive {
			user.IsActive = false
			if err := repo.Update(ctx, user); err != nil {
				t.Fatalf("disable %s: %v", u.email, err)
			}
		}
	}

	n, err := repo.CountActiveAdmins(ctx)
	if err != nil {
		t.Fatalf("CountActiveAdmins: %v", err)
	}
	if n != 1 {
		t.Errorf("CountActiveAdmins() = %d, want 1", n)
	}
}
