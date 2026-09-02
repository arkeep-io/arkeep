package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// reconcileFixture bundles a registered agent, a job and a destination with a
// set of seeded snapshot records, ready to be reconciled.
type reconcileFixture struct {
	ts     *testServer
	agent  *fakeAgent
	jobID  uuid.UUID
	destID uuid.UUID
}

// newReconcileFixture starts a server, registers a fake agent and creates a
// destination plus a job whose policy the snapshots can hang off.
func newReconcileFixture(t *testing.T) *reconcileFixture {
	t.Helper()

	ts := newTestServer(t)
	agent := newFakeAgent(t, ts.addr)
	agentID := agent.register(t)

	agentUUID, err := uuid.Parse(agentID)
	if err != nil {
		t.Fatalf("parse agent id: %v", err)
	}
	job := createIntegrationJob(t, ts, agentUUID)

	dest := &db.Destination{Name: "reconcile-dest", Type: "local"}
	if err := ts.destRepo.Create(context.Background(), dest); err != nil {
		t.Fatalf("create destination: %v", err)
	}

	return &reconcileFixture{ts: ts, agent: agent, jobID: job.ID, destID: dest.ID}
}

// seedSnapshot inserts a snapshot record for the fixture's destination.
func (f *reconcileFixture) seedSnapshot(t *testing.T, destID uuid.UUID, snapshotID string, snapshotAt time.Time) {
	t.Helper()

	job, err := f.ts.jobRepo.GetByID(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	snap := &db.Snapshot{
		PolicyID:      job.PolicyID,
		DestinationID: destID,
		JobID:         &f.jobID,
		SnapshotID:    snapshotID,
		SnapshotAt:    snapshotAt,
		Tags:          "[]",
		Sources:       "[]",
	}
	if err := f.ts.snapshotRepo.Create(context.Background(), snap); err != nil {
		t.Fatalf("create snapshot %s: %v", snapshotID, err)
	}
}

// remainingIDs returns the engine snapshot IDs still recorded for a destination.
func (f *reconcileFixture) remainingIDs(t *testing.T, destID uuid.UUID) []string {
	t.Helper()
	rows, _, err := f.ts.snapshotRepo.ListByDestination(
		context.Background(), destID, repositories.ListOptions{Limit: 100})
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.SnapshotID)
	}
	return ids
}

// TestReportSnapshotReconcileEvictsStaleRecords is the end-to-end regression for
// the reported bug: the Snapshots page kept listing snapshots that retention had
// already pruned from the repository. Once the agent reports the repository's
// live snapshot IDs, the records for everything else must be gone.
func TestReportSnapshotReconcileEvictsStaleRecords(t *testing.T) {
	f := newReconcileFixture(t)
	old := time.Now().UTC().Add(-48 * time.Hour)

	f.seedSnapshot(t, f.destID, "kept-1", old)
	f.seedSnapshot(t, f.destID, "pruned-1", old)
	f.seedSnapshot(t, f.destID, "kept-2", old)

	resp, err := f.agent.client.ReportSnapshotReconcile(context.Background(), &proto.SnapshotReconcileReport{
		AgentId:         f.agent.agentID,
		JobId:           f.jobID.String(),
		DestinationId:   f.destID.String(),
		LiveSnapshotIds: []string{"kept-1", "kept-2"},
		ListedAt:        timestamppb.New(time.Now().UTC()),
	})
	if err != nil {
		t.Fatalf("ReportSnapshotReconcile: %v", err)
	}
	if resp.Deleted != 1 {
		t.Errorf("deleted = %d, want 1", resp.Deleted)
	}

	got := f.remainingIDs(t, f.destID)
	if len(got) != 2 {
		t.Fatalf("remaining = %v, want 2 records", got)
	}
	for _, id := range got {
		if id == "pruned-1" {
			t.Errorf("remaining = %v, still contains the pruned snapshot", got)
		}
	}
}

// TestReportSnapshotReconcileIgnoresEmptyList verifies the guard against
// wiping a destination. The agent only reports a successful listing, so an
// empty list means something went wrong upstream — acting on it would delete
// every record for the destination.
func TestReportSnapshotReconcileIgnoresEmptyList(t *testing.T) {
	f := newReconcileFixture(t)
	old := time.Now().UTC().Add(-48 * time.Hour)

	f.seedSnapshot(t, f.destID, "snap-1", old)
	f.seedSnapshot(t, f.destID, "snap-2", old)

	resp, err := f.agent.client.ReportSnapshotReconcile(context.Background(), &proto.SnapshotReconcileReport{
		AgentId:         f.agent.agentID,
		JobId:           f.jobID.String(),
		DestinationId:   f.destID.String(),
		LiveSnapshotIds: nil,
		ListedAt:        timestamppb.New(time.Now().UTC()),
	})
	if err != nil {
		t.Fatalf("ReportSnapshotReconcile: %v", err)
	}
	if resp.Deleted != 0 {
		t.Errorf("deleted = %d, want 0 — an empty listing must never evict", resp.Deleted)
	}
	if got := f.remainingIDs(t, f.destID); len(got) != 2 {
		t.Errorf("remaining = %v, want both records kept", got)
	}
}

// TestReportSnapshotReconcileKeepsRecordsNewerThanListing verifies that a
// snapshot created after the listing was taken — for instance by a concurrent
// backup from another agent writing to the same repository — is not evicted.
// The reconcile only deletes, so a wrongly evicted record never comes back.
func TestReportSnapshotReconcileKeepsRecordsNewerThanListing(t *testing.T) {
	f := newReconcileFixture(t)

	listedAt := time.Now().UTC().Add(-time.Hour)
	f.seedSnapshot(t, f.destID, "before-listing", listedAt.Add(-time.Hour))
	f.seedSnapshot(t, f.destID, "after-listing", listedAt.Add(time.Minute))

	resp, err := f.agent.client.ReportSnapshotReconcile(context.Background(), &proto.SnapshotReconcileReport{
		AgentId:         f.agent.agentID,
		JobId:           f.jobID.String(),
		DestinationId:   f.destID.String(),
		LiveSnapshotIds: []string{"unrelated"},
		ListedAt:        timestamppb.New(listedAt),
	})
	if err != nil {
		t.Fatalf("ReportSnapshotReconcile: %v", err)
	}
	if resp.Deleted != 1 {
		t.Errorf("deleted = %d, want 1", resp.Deleted)
	}

	got := f.remainingIDs(t, f.destID)
	if len(got) != 1 || got[0] != "after-listing" {
		t.Errorf("remaining = %v, want [after-listing] — records newer than the listing must survive", got)
	}
}

// TestReportSnapshotReconcileUpdatesRepoSize verifies that the post-prune
// repository size reported alongside the listing reaches the destination. The
// destination report sent before retention carries no size precisely so this
// one — measured after the prune — is the figure the GUI shows.
func TestReportSnapshotReconcileUpdatesRepoSize(t *testing.T) {
	f := newReconcileFixture(t)
	f.seedSnapshot(t, f.destID, "snap-1", time.Now().UTC().Add(-time.Hour))

	const wantSize int64 = 4096

	if _, err := f.agent.client.ReportSnapshotReconcile(context.Background(), &proto.SnapshotReconcileReport{
		AgentId:         f.agent.agentID,
		JobId:           f.jobID.String(),
		DestinationId:   f.destID.String(),
		LiveSnapshotIds: []string{"snap-1"},
		ListedAt:        timestamppb.New(time.Now().UTC()),
		RepoSizeBytes:   wantSize,
	}); err != nil {
		t.Fatalf("ReportSnapshotReconcile: %v", err)
	}

	dest, err := f.ts.destRepo.GetByID(context.Background(), f.destID)
	if err != nil {
		t.Fatalf("get destination: %v", err)
	}
	if dest.RepoSizeBytes != wantSize {
		t.Errorf("destination repo_size_bytes = %d, want %d", dest.RepoSizeBytes, wantSize)
	}
}

// TestReportSnapshotReconcileRejectsInvalidDestination verifies that a
// malformed destination_id is rejected rather than silently ignored.
func TestReportSnapshotReconcileRejectsInvalidDestination(t *testing.T) {
	f := newReconcileFixture(t)

	_, err := f.agent.client.ReportSnapshotReconcile(context.Background(), &proto.SnapshotReconcileReport{
		AgentId:         f.agent.agentID,
		JobId:           f.jobID.String(),
		DestinationId:   "not-a-uuid",
		LiveSnapshotIds: []string{"snap-1"},
		ListedAt:        timestamppb.New(time.Now().UTC()),
	})
	if err == nil {
		t.Fatal("expected an error for a malformed destination_id, got nil")
	}
}
