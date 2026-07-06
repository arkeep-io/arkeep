package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

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
	dest := &db.Destination{SoftDelete: db.SoftDelete{Base: db.Base{ID: destID}}, Name: "dest", Type: "local"}
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

// TestUpdateDestinationStatus_Idempotent verifies that calling
// UpdateDestinationStatus twice for the same (job_id, destination_id) pair
// returns nil on the second call instead of ErrNotFound. This matters when an
// agent retries the RPC or when the destination appears twice in the job
// payload due to a duplicate policy_destination entry.
func TestUpdateDestinationStatus_Idempotent(t *testing.T) {
	gormDB := newTestDB(t)
	repo := NewJobRepository(gormDB)
	agentRepo := NewAgentRepository(gormDB)
	policyRepo := NewPolicyRepository(gormDB)
	ctx := context.Background()

	destID := uuid.New()
	jobID := uuid.New()
	now := time.Now().UTC()

	agent := &db.Agent{Name: "test-agent", Hostname: "host", Status: "offline", Labels: "{}"}
	if err := agentRepo.Create(ctx, agent); err != nil {
		t.Fatalf("Create agent: %v", err)
	}
	dest := &db.Destination{SoftDelete: db.SoftDelete{Base: db.Base{ID: destID}}, Name: "dest", Type: "local"}
	if err := gormDB.WithContext(ctx).Create(dest).Error; err != nil {
		t.Fatalf("Create destination: %v", err)
	}
	policy := &db.Policy{AgentID: agent.ID, Name: "p", Schedule: "0 * * * *", Sources: `["/"]`}
	if err := policyRepo.Create(ctx, policy); err != nil {
		t.Fatalf("Create policy: %v", err)
	}
	job := &db.Job{Base: db.Base{ID: jobID}, PolicyID: policy.ID, AgentID: agent.ID, Status: "pending"}
	if err := gormDB.WithContext(ctx).Create(job).Error; err != nil {
		t.Fatalf("Create job: %v", err)
	}
	jd := &db.JobDestination{JobID: jobID, DestinationID: destID, Status: "pending"}
	if err := repo.CreateDestination(ctx, jd); err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	// First call succeeds normally (pending → succeeded).
	if err := repo.UpdateDestinationStatus(ctx, jobID, destID, "succeeded", &now, &now, "snap-aaa", 1000, ""); err != nil {
		t.Fatalf("first UpdateDestinationStatus: %v", err)
	}

	// Second call with same parameters must also return nil (idempotent).
	if err := repo.UpdateDestinationStatus(ctx, jobID, destID, "succeeded", &now, &now, "snap-aaa", 1000, ""); err != nil {
		t.Errorf("second UpdateDestinationStatus (idempotent): got %v, want nil", err)
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

// seedJobWithLogs creates the parent rows (agent, policy, job) and inserts the
// given log lines for that job. Returns the job ID.
func seedJobWithLogs(t *testing.T, gormDB *gorm.DB, repo JobRepository, logs []db.JobLog) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	agent := &db.Agent{Name: "test-agent", Hostname: "host", Status: "offline", Labels: "{}"}
	agentRepo := NewAgentRepository(gormDB)
	if err := agentRepo.Create(ctx, agent); err != nil {
		t.Fatalf("Create agent: %v", err)
	}
	policy := &db.Policy{AgentID: agent.ID, Name: "p", Schedule: "0 * * * *", Sources: `["/"]`}
	if err := NewPolicyRepository(gormDB).Create(ctx, policy); err != nil {
		t.Fatalf("Create policy: %v", err)
	}
	jobID := uuid.New()
	job := &db.Job{Base: db.Base{ID: jobID}, PolicyID: policy.ID, AgentID: agent.ID, Status: "succeeded"}
	if err := gormDB.WithContext(ctx).Create(job).Error; err != nil {
		t.Fatalf("Create job: %v", err)
	}
	for i := range logs {
		logs[i].JobID = jobID
	}
	if err := repo.BulkCreateLogs(ctx, logs); err != nil {
		t.Fatalf("BulkCreateLogs: %v", err)
	}
	return jobID
}

// TestPruneLogsByLevel verifies that pruning removes only rows matching the
// requested levels older than the cutoff, leaving recent rows and other levels
// intact — the core of the log-retention feature.
func TestPruneLogsByLevel(t *testing.T) {
	gormDB := newTestDB(t)
	repo := NewJobRepository(gormDB)
	ctx := context.Background()

	now := time.Now().UTC()
	old := now.Add(-60 * 24 * time.Hour)    // 60 days ago
	recent := now.Add(-1 * 24 * time.Hour)  // yesterday
	cutoff := now.Add(-30 * 24 * time.Hour) // 30 days retention

	jobID := seedJobWithLogs(t, gormDB, repo, []db.JobLog{
		{Level: "info", Message: "old info", Timestamp: old},
		{Level: "info", Message: "recent info", Timestamp: recent},
		{Level: "warn", Message: "old warn", Timestamp: old},
		{Level: "error", Message: "old error", Timestamp: old},
	})

	deleted, err := repo.PruneLogsByLevel(ctx, []string{"info"}, cutoff, 5000)
	if err != nil {
		t.Fatalf("PruneLogsByLevel: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 (only the old info line)", deleted)
	}

	remaining, err := repo.GetLogs(ctx, jobID)
	if err != nil {
		t.Fatalf("GetLogs: %v", err)
	}
	if len(remaining) != 3 {
		t.Fatalf("remaining logs = %d, want 3", len(remaining))
	}
	for _, l := range remaining {
		if l.Level == "info" && l.Message == "old info" {
			t.Errorf("old info line was not pruned")
		}
	}
}

// TestPruneLogsByLevel_Batching verifies the batch loop deletes everything when
// the number of matching rows exceeds a single batch.
func TestPruneLogsByLevel_Batching(t *testing.T) {
	gormDB := newTestDB(t)
	repo := NewJobRepository(gormDB)
	ctx := context.Background()

	now := time.Now().UTC()
	old := now.Add(-60 * 24 * time.Hour)

	logs := make([]db.JobLog, 0, 25)
	for range 25 {
		logs = append(logs, db.JobLog{Level: "info", Message: "old", Timestamp: old})
	}
	jobID := seedJobWithLogs(t, gormDB, repo, logs)

	// batchSize smaller than the dataset forces multiple loop iterations.
	deleted, err := repo.PruneLogsByLevel(ctx, []string{"info"}, now, 10)
	if err != nil {
		t.Fatalf("PruneLogsByLevel: %v", err)
	}
	if deleted != 25 {
		t.Errorf("deleted = %d, want 25", deleted)
	}
	remaining, err := repo.GetLogs(ctx, jobID)
	if err != nil {
		t.Fatalf("GetLogs: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("remaining logs = %d, want 0", len(remaining))
	}
}

// TestReclaimLogSpace verifies VACUUM runs without error on SQLite.
func TestReclaimLogSpace(t *testing.T) {
	repo := NewJobRepository(newTestDB(t))
	if err := repo.ReclaimLogSpace(context.Background()); err != nil {
		t.Fatalf("ReclaimLogSpace: %v", err)
	}
}
