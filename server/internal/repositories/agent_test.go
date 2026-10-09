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

// TestCertFingerprintBinding covers the agent identity binding (SEC-24): the
// first binding wins, a binding survives a Save of a stale copy, one
// certificate binds one live agent, and a reset frees the agent to rebind.
func TestCertFingerprintBinding(t *testing.T) {
	gdb := newTestDB(t)
	repo := NewAgentRepository(gdb)
	ctx := context.Background()

	a := createTestAgent(t, repo, "online", time.Now())
	b := createTestAgent(t, repo, "online", time.Now())

	stale, err := repo.GetByID(ctx, a)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if ok, err := repo.BindCertFingerprint(ctx, a, "fp-a"); err != nil || !ok {
		t.Fatalf("first bind = %v, %v; want true, nil", ok, err)
	}
	if ok, err := repo.BindCertFingerprint(ctx, a, "fp-other"); err != nil || ok {
		t.Errorf("second bind = %v, %v; want false, nil (first binding wins)", ok, err)
	}

	// A Save of a copy read before the binding must not clear it.
	stale.Name = "renamed"
	if err := repo.Update(ctx, stale); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := repo.GetByCertFingerprint(ctx, "fp-a")
	if err != nil || got.ID != a {
		t.Fatalf("GetByCertFingerprint(fp-a) = %v, %v; want agent a", got, err)
	}

	// The same certificate cannot bind a second live agent.
	if _, err := repo.BindCertFingerprint(ctx, b, "fp-a"); err == nil {
		t.Error("binding fp-a to a second agent succeeded, want a unique index error")
	}

	if err := repo.ResetCertFingerprint(ctx, a); err != nil {
		t.Fatalf("ResetCertFingerprint: %v", err)
	}
	if _, err := repo.GetByCertFingerprint(ctx, "fp-a"); err != ErrNotFound {
		t.Errorf("GetByCertFingerprint after reset error = %v, want ErrNotFound", err)
	}
	if ok, err := repo.BindCertFingerprint(ctx, b, "fp-a"); err != nil || !ok {
		t.Errorf("bind fp-a to b after reset = %v, %v; want true, nil", ok, err)
	}

	// A soft-deleted agent keeps its fingerprint but does not hold it.
	if err := repo.Delete(ctx, b); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByCertFingerprint(ctx, "fp-a"); err != ErrNotFound {
		t.Errorf("GetByCertFingerprint of a deleted agent error = %v, want ErrNotFound", err)
	}
	if ok, err := repo.BindCertFingerprint(ctx, a, "fp-a"); err != nil || !ok {
		t.Errorf("rebinding a deleted agent's certificate = %v, %v; want true, nil", ok, err)
	}
	if err := repo.ResetCertFingerprint(ctx, uuid.New()); err != ErrNotFound {
		t.Errorf("ResetCertFingerprint(unknown) error = %v, want ErrNotFound", err)
	}
}
