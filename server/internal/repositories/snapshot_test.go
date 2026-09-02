package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// snapshotFixture holds the parent rows every snapshot needs to satisfy the
// foreign keys (PRAGMA foreign_keys is ON in the test database).
type snapshotFixture struct {
	gormDB *gorm.DB
	repo   SnapshotRepository
	jobID  uuid.UUID
	policy *db.Policy
}

// newSnapshotFixture creates an agent, a policy and a job, plus the number of
// destinations requested, and returns their IDs. Snapshots can then be attached
// to any of them without tripping a foreign key.
func newSnapshotFixture(t *testing.T, destCount int) (*snapshotFixture, []uuid.UUID) {
	t.Helper()

	gormDB := newTestDB(t)
	ctx := context.Background()

	agent := &db.Agent{Name: "test-agent", Hostname: "host", Status: "offline", Labels: "{}"}
	if err := NewAgentRepository(gormDB).Create(ctx, agent); err != nil {
		t.Fatalf("Create agent: %v", err)
	}
	policy := &db.Policy{AgentID: agent.ID, Name: "p", Schedule: "0 * * * *", Sources: `["/"]`}
	if err := NewPolicyRepository(gormDB).Create(ctx, policy); err != nil {
		t.Fatalf("Create policy: %v", err)
	}
	job := &db.Job{PolicyID: &policy.ID, AgentID: agent.ID, Status: "succeeded"}
	if err := gormDB.WithContext(ctx).Create(job).Error; err != nil {
		t.Fatalf("Create job: %v", err)
	}

	destIDs := make([]uuid.UUID, destCount)
	for i := range destIDs {
		dest := &db.Destination{Name: "dest", Type: "local"}
		if err := gormDB.WithContext(ctx).Create(dest).Error; err != nil {
			t.Fatalf("Create destination %d: %v", i, err)
		}
		destIDs[i] = dest.ID
	}

	return &snapshotFixture{
		gormDB: gormDB,
		repo:   NewSnapshotRepository(gormDB),
		jobID:  job.ID,
		policy: policy,
	}, destIDs
}

// createSnapshot inserts a snapshot record for a destination at a given time.
func (f *snapshotFixture) createSnapshot(t *testing.T, destID uuid.UUID, snapshotID string, snapshotAt time.Time) {
	t.Helper()
	snap := &db.Snapshot{
		PolicyID:      &f.policy.ID,
		DestinationID: destID,
		JobID:         &f.jobID,
		SnapshotID:    snapshotID,
		SnapshotAt:    snapshotAt,
		Tags:          "[]",
		Sources:       "[]",
	}
	if err := f.repo.Create(context.Background(), snap); err != nil {
		t.Fatalf("Create snapshot %s: %v", snapshotID, err)
	}
}

// remainingSnapshotIDs returns the engine snapshot IDs still recorded for a
// destination, so tests can assert on exactly what survived.
func (f *snapshotFixture) remainingSnapshotIDs(t *testing.T, destID uuid.UUID) []string {
	t.Helper()
	var snaps []db.Snapshot
	if err := f.gormDB.WithContext(context.Background()).
		Where("destination_id = ?", destID).
		Order("snapshot_id ASC").
		Find(&snaps).Error; err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	ids := make([]string, 0, len(snaps))
	for _, s := range snaps {
		ids = append(ids, s.SnapshotID)
	}
	return ids
}

// TestDeleteStaleByDestination_EvictsOnlyPrunedRecords verifies the core of the
// snapshot reconcile: records whose engine snapshot ID is missing from the
// repository listing are removed, and the ones still present are kept.
func TestDeleteStaleByDestination_EvictsOnlyPrunedRecords(t *testing.T) {
	f, destIDs := newSnapshotFixture(t, 1)
	destID := destIDs[0]
	old := time.Now().UTC().Add(-24 * time.Hour)

	f.createSnapshot(t, destID, "alive-1", old)
	f.createSnapshot(t, destID, "pruned-1", old)
	f.createSnapshot(t, destID, "alive-2", old)
	f.createSnapshot(t, destID, "pruned-2", old)

	deleted, err := f.repo.DeleteStaleByDestination(
		context.Background(), destID, []string{"alive-1", "alive-2"}, time.Now().UTC())
	if err != nil {
		t.Fatalf("DeleteStaleByDestination: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	got := f.remainingSnapshotIDs(t, destID)
	want := []string{"alive-1", "alive-2"}
	if len(got) != len(want) {
		t.Fatalf("remaining = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("remaining = %v, want %v", got, want)
			break
		}
	}
}

// TestDeleteStaleByDestination_KeepsOtherDestinations is the regression case
// for the destination scoping. Engine snapshot IDs are unique per repository,
// not globally: two destinations pointing at byte-identical repositories (an
// rclone-synced bucket, a filesystem copy) hold records with the same ID.
// Reconciling one must never touch the other.
func TestDeleteStaleByDestination_KeepsOtherDestinations(t *testing.T) {
	f, destIDs := newSnapshotFixture(t, 2)
	destA, destB := destIDs[0], destIDs[1]
	old := time.Now().UTC().Add(-24 * time.Hour)

	f.createSnapshot(t, destA, "shared-id", old)
	f.createSnapshot(t, destB, "shared-id", old)

	// Reconcile destination A against a listing that no longer has the snapshot.
	deleted, err := f.repo.DeleteStaleByDestination(
		context.Background(), destA, []string{"something-else"}, time.Now().UTC())
	if err != nil {
		t.Fatalf("DeleteStaleByDestination: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	if got := f.remainingSnapshotIDs(t, destA); len(got) != 0 {
		t.Errorf("destination A remaining = %v, want none", got)
	}
	if got := f.remainingSnapshotIDs(t, destB); len(got) != 1 || got[0] != "shared-id" {
		t.Errorf("destination B remaining = %v, want [shared-id] — the other destination must be untouched", got)
	}
}

// TestDeleteStaleByDestination_KeepsRecordsNewerThanCutoff is the regression
// case for the race with a concurrent backup. Another agent writing to the same
// repository can create a snapshot after the listing was taken; its record must
// survive, because the reconcile only deletes and would never bring it back.
func TestDeleteStaleByDestination_KeepsRecordsNewerThanCutoff(t *testing.T) {
	f, destIDs := newSnapshotFixture(t, 1)
	destID := destIDs[0]

	cutoff := time.Now().UTC()
	f.createSnapshot(t, destID, "before-listing", cutoff.Add(-time.Hour))
	f.createSnapshot(t, destID, "after-listing", cutoff.Add(time.Hour))

	// Neither ID is in the listing, but only the older one may be evicted.
	deleted, err := f.repo.DeleteStaleByDestination(
		context.Background(), destID, []string{"unrelated"}, cutoff)
	if err != nil {
		t.Fatalf("DeleteStaleByDestination: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	got := f.remainingSnapshotIDs(t, destID)
	if len(got) != 1 || got[0] != "after-listing" {
		t.Errorf("remaining = %v, want [after-listing] — records newer than the cutoff must survive", got)
	}
}

// TestDeleteStaleByDestination_NoStaleRecords verifies that a fully in-sync
// destination is a no-op, which is the common case on every backup run.
func TestDeleteStaleByDestination_NoStaleRecords(t *testing.T) {
	f, destIDs := newSnapshotFixture(t, 1)
	destID := destIDs[0]
	old := time.Now().UTC().Add(-24 * time.Hour)

	f.createSnapshot(t, destID, "alive-1", old)
	f.createSnapshot(t, destID, "alive-2", old)

	deleted, err := f.repo.DeleteStaleByDestination(
		context.Background(), destID, []string{"alive-1", "alive-2"}, time.Now().UTC())
	if err != nil {
		t.Fatalf("DeleteStaleByDestination: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}
	if got := f.remainingSnapshotIDs(t, destID); len(got) != 2 {
		t.Errorf("remaining = %v, want both records kept", got)
	}
}

// TestCreateImportedSnapshot covers snapshots imported from a pre-existing
// Restic repository. Such snapshots belong to a destination but to no policy
// and no job — they were not produced by a backup run — so policy_id and
// job_id must be storable as NULL.
func TestCreateImportedSnapshot(t *testing.T) {
	gdb := newTestDB(t)
	repo := NewSnapshotRepository(gdb)
	destRepo := NewDestinationRepository(gdb)
	ctx := context.Background()

	dest := &db.Destination{
		Name:        "imported-repo",
		Type:        "rclone",
		Credentials: db.EncryptedString(`{"config":"x"}`),
		Config:      `{}`,
		Enabled:     true,
	}
	if err := destRepo.Create(ctx, dest); err != nil {
		t.Fatalf("Create destination: %v", err)
	}

	snap := &db.Snapshot{
		PolicyID:      nil,
		JobID:         nil,
		DestinationID: dest.ID,
		IsImported:    true,
		SnapshotID:    "1a2b3c4d5e6f",
		Hostname:      "nas",
		Sources:       `["/data"]`,
		Tags:          `[]`,
		SnapshotAt:    time.Now().UTC(),
	}
	if err := repo.Create(ctx, snap); err != nil {
		t.Fatalf("Create imported snapshot: %v", err)
	}

	// The snapshot must be listable for its destination, with an empty policy
	// name (the LEFT JOIN finds no policy row).
	rows, total, err := repo.ListByDestination(ctx, dest.ID, ListOptions{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListByDestination: %v", err)
	}
	if total != 1 {
		t.Fatalf("ListByDestination total = %d, want 1", total)
	}
	if len(rows) != 1 {
		t.Fatalf("ListByDestination returned %d rows, want 1", len(rows))
	}
	if rows[0].SnapshotID != "1a2b3c4d5e6f" {
		t.Errorf("SnapshotID = %q, want %q", rows[0].SnapshotID, "1a2b3c4d5e6f")
	}
	if !rows[0].IsImported {
		t.Error("IsImported = false, want true")
	}
	if rows[0].PolicyName != "" {
		t.Errorf("PolicyName = %q, want empty", rows[0].PolicyName)
	}
}

// TestExistsBySnapshotIDAndDestination verifies that the import dedup check is
// scoped per destination: the same Restic snapshot ID copied into a second
// destination (e.g. after migrating a repository to another provider) must not
// be reported as already present.
func TestExistsBySnapshotIDAndDestination(t *testing.T) {
	gdb := newTestDB(t)
	repo := NewSnapshotRepository(gdb)
	destRepo := NewDestinationRepository(gdb)
	ctx := context.Background()

	newDest := func(name string) *db.Destination {
		d := &db.Destination{
			Name:        name,
			Type:        "rclone",
			Credentials: db.EncryptedString(`{"config":"x"}`),
			Config:      `{}`,
			Enabled:     true,
		}
		if err := destRepo.Create(ctx, d); err != nil {
			t.Fatalf("Create destination %s: %v", name, err)
		}
		return d
	}
	oldDest, newProvider := newDest("provider1"), newDest("provider2")

	const resticID = "deadbeefcafe"
	snap := &db.Snapshot{
		PolicyID:      nil,
		JobID:         nil,
		DestinationID: oldDest.ID,
		IsImported:    true,
		SnapshotID:    resticID,
		Sources:       `["/data"]`,
		Tags:          `[]`,
		SnapshotAt:    time.Now().UTC(),
	}
	if err := repo.Create(ctx, snap); err != nil {
		t.Fatalf("Create imported snapshot: %v", err)
	}

	exists, err := repo.ExistsBySnapshotIDAndDestination(ctx, resticID, oldDest.ID)
	if err != nil {
		t.Fatalf("ExistsBySnapshotIDAndDestination(oldDest): %v", err)
	}
	if !exists {
		t.Error("exists on original destination = false, want true")
	}

	exists, err = repo.ExistsBySnapshotIDAndDestination(ctx, resticID, newProvider.ID)
	if err != nil {
		t.Fatalf("ExistsBySnapshotIDAndDestination(newProvider): %v", err)
	}
	if exists {
		t.Error("exists on the new provider's destination = true, want false: the same repository copied to another provider must still be importable")
	}
}
