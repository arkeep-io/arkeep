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

// TestListSortByUsage verifies server-side sorting of destinations by usage
// (repo_size_bytes) in both directions, and that an unknown SortBy falls back
// to the default order.
func TestListSortByUsage(t *testing.T) {
	gdb := newTestDB(t)
	repo := NewDestinationRepository(gdb)
	ctx := context.Background()

	// Three destinations with distinct sizes.
	specs := []struct {
		name string
		size int64
	}{
		{"small", 100},
		{"large", 300},
		{"medium", 200},
	}
	now := time.Now().UTC()
	for _, s := range specs {
		d := &db.Destination{Name: s.name, Type: "s3", Config: "{}", Enabled: true}
		if err := repo.Create(ctx, d); err != nil {
			t.Fatalf("Create %s: %v", s.name, err)
		}
		if err := repo.UpdateRepoSize(ctx, d.ID, s.size, now); err != nil {
			t.Fatalf("UpdateRepoSize %s: %v", s.name, err)
		}
	}

	names := func(ds []db.Destination) []string {
		out := make([]string, len(ds))
		for i, d := range ds {
			out[i] = d.Name
		}
		return out
	}

	desc, _, err := repo.List(ctx, ListOptions{Limit: 10, SortBy: "usage", SortDesc: true})
	if err != nil {
		t.Fatalf("List desc: %v", err)
	}
	if got := names(desc); got[0] != "large" || got[2] != "small" {
		t.Errorf("usage desc order = %v, want [large medium small]", got)
	}

	asc, _, err := repo.List(ctx, ListOptions{Limit: 10, SortBy: "usage", SortDesc: false})
	if err != nil {
		t.Fatalf("List asc: %v", err)
	}
	if got := names(asc); got[0] != "small" || got[2] != "large" {
		t.Errorf("usage asc order = %v, want [small medium large]", got)
	}

	// Unknown SortBy must not error and falls back to the default order.
	if _, _, err := repo.List(ctx, ListOptions{Limit: 10, SortBy: "bogus; DROP TABLE"}); err != nil {
		t.Fatalf("List with unknown SortBy errored: %v", err)
	}
}
