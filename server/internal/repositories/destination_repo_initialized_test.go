package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// TestRepoInitialized covers the repository-exists marker: the first mark is
// kept, Save on a stale copy neither sets nor clears it, and a reset clears it.
func TestRepoInitialized(t *testing.T) {
	gdb := newTestDB(t)
	repo := NewDestinationRepository(gdb)
	ctx := context.Background()

	dest := &db.Destination{Name: "nas", Type: "local", Config: `{"path":"/mnt/backup"}`, Enabled: true}
	if err := repo.Create(ctx, dest); err != nil {
		t.Fatalf("Create: %v", err)
	}
	load := func() *db.Destination {
		t.Helper()
		got, err := repo.GetByID(ctx, dest.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		return got
	}
	if load().RepoInitializedAt != nil {
		t.Fatal("new destination is already marked initialized")
	}

	first := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)
	stale := load()
	if err := repo.MarkRepoInitialized(ctx, dest.ID, first); err != nil {
		t.Fatalf("MarkRepoInitialized: %v", err)
	}
	if err := repo.MarkRepoInitialized(ctx, dest.ID, first.Add(time.Hour)); err != nil {
		t.Fatalf("MarkRepoInitialized again: %v", err)
	}
	if got := load().RepoInitializedAt; got == nil || !got.Equal(first) {
		t.Fatalf("RepoInitializedAt = %v, want the first mark %v", got, first)
	}

	// An edit saved from a copy loaded before the mark must not undo it.
	stale.Name = "renamed"
	if err := repo.Update(ctx, stale); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := load(); got.Name != "renamed" || got.RepoInitializedAt == nil {
		t.Fatalf("after Update: name = %q, RepoInitializedAt = %v, want the rename and the mark kept", got.Name, got.RepoInitializedAt)
	}

	if err := repo.ResetRepoInitialized(ctx, dest.ID); err != nil {
		t.Fatalf("ResetRepoInitialized: %v", err)
	}
	if got := load().RepoInitializedAt; got != nil {
		t.Fatalf("RepoInitializedAt after reset = %v, want nil", got)
	}

	if err := repo.ResetRepoInitialized(ctx, uuid.Must(uuid.NewV7())); !errors.Is(err, ErrNotFound) {
		t.Errorf("ResetRepoInitialized(unknown) = %v, want ErrNotFound", err)
	}
}
