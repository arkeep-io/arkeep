package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// TestUpdateRepoSizeAndDashboardTotal verifies that UpdateRepoSize persists the
// real repository size per destination and that the dashboard total sums those
// destination sizes (not the inflated per-snapshot logical sizes).
func TestUpdateRepoSizeAndDashboardTotal(t *testing.T) {
	gdb := newTestDB(t)
	destRepo := NewDestinationRepository(gdb)
	dashRepo := NewDashboardRepository(gdb)
	ctx := context.Background()

	d1 := &db.Destination{Name: "s3-a", Type: "s3", Config: "{}", Enabled: true}
	d2 := &db.Destination{Name: "s3-b", Type: "s3", Config: "{}", Enabled: true}
	for _, d := range []*db.Destination{d1, d2} {
		if err := destRepo.Create(ctx, d); err != nil {
			t.Fatalf("Create destination: %v", err)
		}
	}

	now := time.Now().UTC()
	if err := destRepo.UpdateRepoSize(ctx, d1.ID, 455_000_000_000, now); err != nil {
		t.Fatalf("UpdateRepoSize d1: %v", err)
	}
	if err := destRepo.UpdateRepoSize(ctx, d2.ID, 455_000_000_000, now); err != nil {
		t.Fatalf("UpdateRepoSize d2: %v", err)
	}

	// The size and timestamp must be persisted on the destination.
	got, err := destRepo.GetByID(ctx, d1.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.RepoSizeBytes != 455_000_000_000 {
		t.Errorf("RepoSizeBytes = %d, want 455000000000", got.RepoSizeBytes)
	}
	if got.RepoSizeUpdatedAt == nil {
		t.Error("RepoSizeUpdatedAt is nil, want set")
	}

	// Dashboard total is the sum across destinations, not per-snapshot logical size.
	stats, err := dashRepo.GetStats(ctx)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.SnapshotsTotalSize != 910_000_000_000 {
		t.Errorf("SnapshotsTotalSize = %d, want 910000000000", stats.SnapshotsTotalSize)
	}
}
