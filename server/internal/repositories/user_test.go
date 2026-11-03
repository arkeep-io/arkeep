package repositories

import (
	"context"
	"fmt"
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

// TestUserRepository_ListActiveAdmins: every active admin is returned, however
// many users come before it — the notification service relies on it to reach
// all of them.
func TestUserRepository_ListActiveAdmins(t *testing.T) {
	gdb := newTestDB(t)
	repo := NewUserRepository(gdb)
	ctx := context.Background()

	for i := range 120 {
		email := fmt.Sprintf("user%03d@test.local", i)
		if err := repo.Create(ctx, &db.User{Email: email, DisplayName: email, Role: "user", IsActive: true}); err != nil {
			t.Fatalf("create %s: %v", email, err)
		}
	}
	admin := &db.User{Email: "admin@test.local", DisplayName: "A", Role: "admin", IsActive: true}
	disabled := &db.User{Email: "disabled-admin@test.local", DisplayName: "D", Role: "admin", IsActive: true}
	for _, u := range []*db.User{admin, disabled} {
		if err := repo.Create(ctx, u); err != nil {
			t.Fatalf("create %s: %v", u.Email, err)
		}
	}
	disabled.IsActive = false
	if err := repo.Update(ctx, disabled); err != nil {
		t.Fatalf("disable: %v", err)
	}

	got, err := repo.ListActiveAdmins(ctx)
	if err != nil {
		t.Fatalf("ListActiveAdmins: %v", err)
	}
	if len(got) != 1 || got[0].ID != admin.ID {
		t.Errorf("ListActiveAdmins() = %d users, want only %s", len(got), admin.Email)
	}
}
