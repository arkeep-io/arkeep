package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// createTestAgent inserts an agent with the given status and last_seen_at,
// returning its ID.
func createTestAgent(t *testing.T, repo AgentRepository, status string, lastSeenAt time.Time) uuid.UUID {
	t.Helper()
	agent := &db.Agent{
		Name:       "test-agent",
		Hostname:   "host",
		Status:     status,
		LastSeenAt: &lastSeenAt,
	}
	if err := repo.Create(context.Background(), agent); err != nil {
		t.Fatalf("Create agent: %v", err)
	}
	return agent.ID
}

// TestMarkOfflineIfStale_FlipsAStaleOnlineAgent verifies the core transition:
// an online agent whose last_seen_at predates the cutoff is flipped offline.
func TestMarkOfflineIfStale_FlipsAStaleOnlineAgent(t *testing.T) {
	repo := NewAgentRepository(newTestDB(t))
	cutoff := time.Now().UTC()
	id := createTestAgent(t, repo, "online", cutoff.Add(-time.Hour))

	ok, err := repo.MarkOfflineIfStale(context.Background(), id, cutoff)
	if err != nil {
		t.Fatalf("MarkOfflineIfStale: %v", err)
	}
	if !ok {
		t.Fatal("MarkOfflineIfStale() = false, want true for a stale online agent")
	}

	agent, err := repo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if agent.Status != "offline" {
		t.Errorf("Status = %q, want %q", agent.Status, "offline")
	}
}

// TestMarkOfflineIfStale_KeepsARecentlySeenAgent is the regression case for the
// race with a live heartbeat: an agent whose last_seen_at is AFTER the cutoff
// (it heartbeated again between the caller's ListStale read and this write)
// must not be flipped, even if it was passed in as a candidate.
func TestMarkOfflineIfStale_KeepsARecentlySeenAgent(t *testing.T) {
	repo := NewAgentRepository(newTestDB(t))
	cutoff := time.Now().UTC()
	id := createTestAgent(t, repo, "online", cutoff.Add(time.Minute))

	ok, err := repo.MarkOfflineIfStale(context.Background(), id, cutoff)
	if err != nil {
		t.Fatalf("MarkOfflineIfStale: %v", err)
	}
	if ok {
		t.Fatal("MarkOfflineIfStale() = true, want false — the agent heartbeated after the cutoff")
	}

	agent, err := repo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if agent.Status != "online" {
		t.Errorf("Status = %q, want %q — a recently-seen agent must not be flipped", agent.Status, "online")
	}
}

// TestMarkOfflineIfStale_AlreadyOfflineIsANoop verifies idempotency: calling
// it again on an already-offline agent reports no transition.
func TestMarkOfflineIfStale_AlreadyOfflineIsANoop(t *testing.T) {
	repo := NewAgentRepository(newTestDB(t))
	cutoff := time.Now().UTC()
	id := createTestAgent(t, repo, "offline", cutoff.Add(-time.Hour))

	ok, err := repo.MarkOfflineIfStale(context.Background(), id, cutoff)
	if err != nil {
		t.Fatalf("MarkOfflineIfStale: %v", err)
	}
	if ok {
		t.Error("MarkOfflineIfStale() = true, want false — the agent was already offline")
	}
}

// TestListStale_ReturnsOnlyOnlineAgentsPastCutoff verifies the candidate query:
// only online agents whose last_seen_at predates the cutoff come back.
func TestListStale_ReturnsOnlyOnlineAgentsPastCutoff(t *testing.T) {
	repo := NewAgentRepository(newTestDB(t))
	cutoff := time.Now().UTC()

	staleID := createTestAgent(t, repo, "online", cutoff.Add(-time.Hour))
	createTestAgent(t, repo, "online", cutoff.Add(time.Minute)) // recently seen
	createTestAgent(t, repo, "offline", cutoff.Add(-time.Hour)) // already offline

	stale, err := repo.ListStale(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("ListStale: %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("ListStale() returned %d agents, want 1: %+v", len(stale), stale)
	}
	if stale[0].ID != staleID {
		t.Errorf("ListStale() returned agent %s, want %s", stale[0].ID, staleID)
	}
}
