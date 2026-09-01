package repositories

import (
	"context"
	"errors"
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

// jobFixture inserts the agent, policy and destination a job needs to satisfy
// the FK constraints, and returns their IDs.
type jobFixture struct {
	agentID  uuid.UUID
	policyID uuid.UUID
	destID   uuid.UUID
}

func newJobFixture(t *testing.T, gormDB *gorm.DB) jobFixture {
	t.Helper()
	ctx := context.Background()

	agent := &db.Agent{Name: "test-agent", Hostname: "host", Status: "online", Labels: "{}"}
	if err := NewAgentRepository(gormDB).Create(ctx, agent); err != nil {
		t.Fatalf("Create agent: %v", err)
	}
	policy := &db.Policy{AgentID: agent.ID, Name: "p", Schedule: "0 * * * *", Sources: `["/"]`}
	if err := NewPolicyRepository(gormDB).Create(ctx, policy); err != nil {
		t.Fatalf("Create policy: %v", err)
	}
	dest := &db.Destination{Name: "dest", Type: "local"}
	if err := gormDB.WithContext(ctx).Create(dest).Error; err != nil {
		t.Fatalf("Create destination: %v", err)
	}
	return jobFixture{agentID: agent.ID, policyID: policy.ID, destID: dest.ID}
}

// TestUpdateStatus_RefusesTerminalStates covers the late report: an agent whose
// connection dropped keeps running restic and reports the outcome after the server
// has already recorded the job as interrupted. That report must not resurrect it.
func TestUpdateStatus_RefusesTerminalStates(t *testing.T) {
	for _, terminal := range []string{"succeeded", "failed", "cancelled", "interrupted"} {
		t.Run("from "+terminal, func(t *testing.T) {
			gormDB := newTestDB(t)
			repo := NewJobRepository(gormDB)
			f := newJobFixture(t, gormDB)
			ctx := context.Background()

			job := &db.Job{PolicyID: f.policyID, AgentID: f.agentID, Status: "running"}
			if err := repo.Create(ctx, job); err != nil {
				t.Fatalf("Create job: %v", err)
			}
			now := time.Now().UTC()
			if err := repo.UpdateStatus(ctx, job.ID, terminal, nil, &now, "first outcome"); err != nil {
				t.Fatalf("UpdateStatus to %s: %v", terminal, err)
			}

			err := repo.UpdateStatus(ctx, job.ID, "succeeded", nil, &now, "")
			if !errors.Is(err, ErrTerminalState) {
				t.Fatalf("UpdateStatus after %s returned %v, want ErrTerminalState", terminal, err)
			}

			stored, err := repo.GetByID(ctx, job.ID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if stored.Status != terminal {
				t.Errorf("status = %q, want it left at %q", stored.Status, terminal)
			}
			if stored.Error != "first outcome" {
				t.Errorf("error = %q, want the original %q", stored.Error, "first outcome")
			}
		})
	}
}

// TestUpdateStatus_NotFound keeps ErrNotFound distinguishable from ErrTerminalState.
func TestUpdateStatus_NotFound(t *testing.T) {
	gormDB := newTestDB(t)
	repo := NewJobRepository(gormDB)
	now := time.Now().UTC()

	err := repo.UpdateStatus(context.Background(), uuid.New(), "succeeded", nil, &now, "")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateStatus on a missing job returned %v, want ErrNotFound", err)
	}
}

// TestMarkRunningJobsInterrupted covers the startup sweep: a job left running by a
// dead server process, together with its destination rows.
func TestMarkRunningJobsInterrupted(t *testing.T) {
	gormDB := newTestDB(t)
	repo := NewJobRepository(gormDB)
	f := newJobFixture(t, gormDB)
	ctx := context.Background()

	running := &db.Job{PolicyID: f.policyID, AgentID: f.agentID, Status: "running"}
	if err := repo.Create(ctx, running); err != nil {
		t.Fatalf("Create running job: %v", err)
	}
	if err := repo.CreateDestination(ctx, &db.JobDestination{JobID: running.ID, DestinationID: f.destID, Status: "pending"}); err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	// A job that already finished must be left alone.
	done := &db.Job{PolicyID: f.policyID, AgentID: f.agentID, Status: "succeeded"}
	if err := repo.Create(ctx, done); err != nil {
		t.Fatalf("Create finished job: %v", err)
	}

	n, err := repo.MarkRunningJobsInterrupted(ctx, "server restarted")
	if err != nil {
		t.Fatalf("MarkRunningJobsInterrupted: %v", err)
	}
	if n != 1 {
		t.Fatalf("marked %d jobs, want 1", n)
	}

	stored, err := repo.GetByID(ctx, running.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.Status != "interrupted" {
		t.Errorf("status = %q, want \"interrupted\"", stored.Status)
	}
	if stored.Error != "server restarted" {
		t.Errorf("error = %q, want %q", stored.Error, "server restarted")
	}
	if stored.EndedAt == nil {
		t.Error("EndedAt is nil, want it set")
	}

	dests, err := repo.ListDestinationsByJob(ctx, running.ID)
	if err != nil {
		t.Fatalf("ListDestinationsByJob: %v", err)
	}
	if len(dests) != 1 || dests[0].Status != "interrupted" {
		t.Errorf("destination statuses = %+v, want a single \"interrupted\" row", dests)
	}

	untouched, err := repo.GetByID(ctx, done.ID)
	if err != nil {
		t.Fatalf("GetByID(finished): %v", err)
	}
	if untouched.Status != "succeeded" {
		t.Errorf("finished job status = %q, want it left at \"succeeded\"", untouched.Status)
	}
}

// TestListByAgentAndStatus_FindsOldJobsBeyondTheFirstPage is the regression case
// for filtering in Go over the most-recent page: an agent with a long history
// would hide an older pending job, which then never got dispatched.
func TestListByAgentAndStatus_FindsOldJobsBeyondTheFirstPage(t *testing.T) {
	gormDB := newTestDB(t)
	repo := NewJobRepository(gormDB)
	f := newJobFixture(t, gormDB)
	ctx := context.Background()

	oldPending := &db.Job{PolicyID: f.policyID, AgentID: f.agentID, Status: "pending"}
	if err := repo.Create(ctx, oldPending); err != nil {
		t.Fatalf("Create pending job: %v", err)
	}
	// 120 later jobs of other statuses bury it well past the first page of 100.
	for i := 0; i < 120; i++ {
		j := &db.Job{PolicyID: f.policyID, AgentID: f.agentID, Status: "succeeded"}
		if err := repo.Create(ctx, j); err != nil {
			t.Fatalf("Create filler job %d: %v", i, err)
		}
	}

	got, err := repo.ListByAgentAndStatus(ctx, f.agentID, "pending", ListOptions{Limit: 100})
	if err != nil {
		t.Fatalf("ListByAgentAndStatus: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("found %d pending jobs, want 1", len(got))
	}
	if got[0].ID != oldPending.ID {
		t.Errorf("found job %s, want the buried pending job %s", got[0].ID, oldPending.ID)
	}
}

// TestHasJobForPolicyAfter covers the guard that stops a resume once a later
// scheduled run has already covered the same data.
func TestHasJobForPolicyAfter(t *testing.T) {
	gormDB := newTestDB(t)
	repo := NewJobRepository(gormDB)
	f := newJobFixture(t, gormDB)
	ctx := context.Background()

	first := &db.Job{PolicyID: f.policyID, AgentID: f.agentID, Status: "interrupted"}
	if err := repo.Create(ctx, first); err != nil {
		t.Fatalf("Create first job: %v", err)
	}

	has, err := repo.HasJobForPolicyAfter(ctx, f.policyID, first.CreatedAt)
	if err != nil {
		t.Fatalf("HasJobForPolicyAfter: %v", err)
	}
	if has {
		t.Error("reported a newer job when the interrupted one is the only job")
	}

	later := &db.Job{PolicyID: f.policyID, AgentID: f.agentID, Status: "pending"}
	if err := repo.Create(ctx, later); err != nil {
		t.Fatalf("Create later job: %v", err)
	}
	if err := gormDB.Model(later).Update("created_at", first.CreatedAt.Add(time.Hour)).Error; err != nil {
		t.Fatalf("age later job: %v", err)
	}

	has, err = repo.HasJobForPolicyAfter(ctx, f.policyID, first.CreatedAt)
	if err != nil {
		t.Fatalf("HasJobForPolicyAfter: %v", err)
	}
	if !has {
		t.Error("did not report the newer job")
	}
}
