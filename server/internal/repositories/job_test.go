package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// TestUpdateDestinationStatus_MultipleJobsSameDestination verifies that two
// jobs sharing the same destination can both report their results without
// interfering with each other. This is the regression case for the bug where
// the previous implementation resolved the JobDestination PK via a LEFT JOIN
// scan, which could resolve the wrong row when multiple jobs shared a
// destination, causing the UPDATE to match 0 rows.
func TestUpdateDestinationStatus_MultipleJobsSameDestination(t *testing.T) {
	gormDB := newTestDB(t)
	repo := NewJobRepository(gormDB)
	agentRepo := NewAgentRepository(gormDB)
	policyRepo := NewPolicyRepository(gormDB)
	ctx := context.Background()

	destID := uuid.New()
	jobAID := uuid.New()
	jobBID := uuid.New()

	now := time.Now().UTC()

	// Insert the parent rows required by FK constraints.
	agent := &db.Agent{Name: "test-agent", Hostname: "host", Status: "offline", Labels: "{}"}
	if err := agentRepo.Create(ctx, agent); err != nil {
		t.Fatalf("Create agent: %v", err)
	}
	dest := &db.Destination{Base: db.Base{ID: destID}, Name: "dest", Type: "local"}
	if err := gormDB.WithContext(ctx).Create(dest).Error; err != nil {
		t.Fatalf("Create destination: %v", err)
	}
	policy := &db.Policy{AgentID: agent.ID, Name: "p", Schedule: "0 * * * *", Sources: `["/"]`}
	if err := policyRepo.Create(ctx, policy); err != nil {
		t.Fatalf("Create policy: %v", err)
	}
	for _, jobID := range []uuid.UUID{jobAID, jobBID} {
		job := &db.Job{Base: db.Base{ID: jobID}, PolicyID: policy.ID, AgentID: agent.ID, Status: "pending"}
		if err := gormDB.WithContext(ctx).Create(job).Error; err != nil {
			t.Fatalf("Create job %s: %v", jobID, err)
		}
	}

	for _, jd := range []*db.JobDestination{
		{JobID: jobAID, DestinationID: destID, Status: "pending"},
		{JobID: jobBID, DestinationID: destID, Status: "pending"},
	} {
		if err := repo.CreateDestination(ctx, jd); err != nil {
			t.Fatalf("CreateDestination: %v", err)
		}
	}

	// Both jobs should be updatable independently.
	if err := repo.UpdateDestinationStatus(ctx, jobAID, destID, "succeeded", &now, &now, "snap-aaa", 1000, ""); err != nil {
		t.Fatalf("UpdateDestinationStatus jobA: %v", err)
	}
	if err := repo.UpdateDestinationStatus(ctx, jobBID, destID, "succeeded", &now, &now, "snap-bbb", 2000, ""); err != nil {
		t.Fatalf("UpdateDestinationStatus jobB: %v", err)
	}

	// Confirm both records have the expected status.
	dests, err := repo.ListDestinationsByJob(ctx, jobAID)
	if err != nil {
		t.Fatalf("ListDestinationsByJob jobA: %v", err)
	}
	if len(dests) != 1 || dests[0].Status != "succeeded" {
		t.Errorf("jobA destination: got %+v, want status=succeeded", dests)
	}

	dests, err = repo.ListDestinationsByJob(ctx, jobBID)
	if err != nil {
		t.Fatalf("ListDestinationsByJob jobB: %v", err)
	}
	if len(dests) != 1 || dests[0].Status != "succeeded" {
		t.Errorf("jobB destination: got %+v, want status=succeeded", dests)
	}
}

// TestUpdateDestinationStatus_NotFound verifies that updating a non-existent
// job_destination row returns ErrNotFound.
func TestUpdateDestinationStatus_NotFound(t *testing.T) {
	repo := NewJobRepository(newTestDB(t))
	ctx := context.Background()

	now := time.Now().UTC()
	err := repo.UpdateDestinationStatus(ctx, uuid.New(), uuid.New(), "succeeded", &now, &now, "x", 0, "")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
