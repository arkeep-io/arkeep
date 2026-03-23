package repositories

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// newTestDB opens a fresh in-memory SQLite database with all migrations
// applied. InitEncryption is called with a fixed test key so that
// EncryptedString fields can be written and read without errors.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	// Fixed 32-byte key — value is arbitrary, just needs to be stable.
	if err := db.InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("db.InitEncryption: %v", err)
	}
	gormDB, err := db.New(db.Config{
		Driver:   "sqlite",
		DSN:      ":memory:",
		Logger:   zap.NewNop(),
		LogLevel: gormlogger.Silent,
	})
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	return gormDB
}

func TestActivePoliciesCount(t *testing.T) {
	repo := NewPolicyRepository(newTestDB(t))
	ctx := context.Background()

	agentID := uuid.New()
	newPolicy := func() *db.Policy {
		return &db.Policy{
			Name:     "test-policy",
			AgentID:  agentID,
			Schedule: "0 2 * * *",
			Enabled:  true,
			Sources:  `["/data"]`,
		}
	}

	// Create 3 enabled policies, then disable one.
	// GORM's Create skips zero-value booleans and falls back to the column
	// default (true), so we use Update/Save to reliably persist Enabled=false.
	p1, p2, p3 := newPolicy(), newPolicy(), newPolicy()
	for _, p := range []*db.Policy{p1, p2, p3} {
		if err := repo.Create(ctx, p); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	p3.Enabled = false
	if err := repo.Update(ctx, p3); err != nil {
		t.Fatalf("Update (disable p3): %v", err)
	}

	if got := repo.ActivePoliciesCount(ctx); got != 2 {
		t.Errorf("ActivePoliciesCount() = %d, want 2", got)
	}

	// Soft-delete a fourth enabled policy — count must remain 2.
	p4 := newPolicy()
	if err := repo.Create(ctx, p4); err != nil {
		t.Fatalf("Create p4: %v", err)
	}
	if err := repo.Delete(ctx, p4.ID); err != nil {
		t.Fatalf("Delete p4: %v", err)
	}
	if got := repo.ActivePoliciesCount(ctx); got != 2 {
		t.Errorf("after soft-delete: ActivePoliciesCount() = %d, want 2", got)
	}
}
