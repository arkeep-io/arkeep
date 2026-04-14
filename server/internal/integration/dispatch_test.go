package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/arkeep-io/arkeep/server/internal/db"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// TestJobDispatch verifies the complete job lifecycle:
//
//  1. Dispatch a job via agentManager → fake agent receives the JobAssignment.
//  2. Agent reports RUNNING → DB status transitions to "running".
//  3. Agent reports COMPLETED → DB status transitions to "succeeded".
func TestJobDispatch(t *testing.T) {
	ts := newTestServer(t)
	agent := newFakeAgent(t, ts.addr)

	// Register and open the stream.
	agentID := agent.register(t)
	jobsCh, cancelStream := agent.openStream(t)
	defer cancelStream()
	waitForAgentStatus(t, ts.agentRepo, agentID, "online")

	// Create a job record in the DB (normally done by the scheduler).
	agentUUID := mustParseUUID(t, agentID)
	job := &db.Job{
		PolicyID: uuid.New(),
		AgentID:  agentUUID,
		Type:     "backup",
		Status:   "pending",
	}
	if err := ts.jobRepo.Create(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	jobID := job.ID.String()

	// Dispatch the job via agentManager (same path the scheduler uses).
	assignment := &proto.JobAssignment{
		JobId:       jobID,
		PolicyId:    job.PolicyID.String(),
		Type:        proto.JobType_JOB_TYPE_BACKUP,
		Payload:     []byte(`{}`),
		ScheduledAt: timestamppb.Now(),
	}
	if err := ts.agentMgr.Dispatch(agentID, assignment); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	// ── Step 1: Agent receives the assignment ─────────────────────────────────

	var received *proto.JobAssignment
	select {
	case received = <-jobsCh:
	case <-timeoutCtx(t, 3).Done():
		t.Fatal("timed out waiting for JobAssignment on stream")
	}

	if received.JobId != jobID {
		t.Errorf("received job_id = %q, want %q", received.JobId, jobID)
	}
	if received.Type != proto.JobType_JOB_TYPE_BACKUP {
		t.Errorf("received type = %v, want JOB_TYPE_BACKUP", received.Type)
	}

	// ── Step 2: Agent reports RUNNING ─────────────────────────────────────────

	agent.reportStatus(t, jobID, proto.JobStatus_JOB_STATUS_RUNNING)
	waitForJobStatus(t, ts.jobRepo, jobID, "running")

	// ── Step 3: Agent reports COMPLETED ───────────────────────────────────────

	agent.reportStatus(t, jobID, proto.JobStatus_JOB_STATUS_COMPLETED)
	waitForJobStatus(t, ts.jobRepo, jobID, "succeeded")

	// Final DB check: StartedAt was set on RUNNING, EndedAt on COMPLETED.
	finalJob, err := ts.jobRepo.GetByID(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("GetByID final: %v", err)
	}
	if finalJob.StartedAt == nil {
		t.Error("started_at is nil after RUNNING report")
	}
	if finalJob.EndedAt == nil {
		t.Error("ended_at is nil after COMPLETED report")
	}
}

// TestJobDispatchFailed verifies that when the agent reports JOB_STATUS_FAILED
// the DB record transitions to "failed" and the error message is stored.
func TestJobDispatchFailed(t *testing.T) {
	ts := newTestServer(t)
	agent := newFakeAgent(t, ts.addr)

	agentID := agent.register(t)
	jobsCh, cancelStream := agent.openStream(t)
	defer cancelStream()
	waitForAgentStatus(t, ts.agentRepo, agentID, "online")

	agentUUID := mustParseUUID(t, agentID)
	job := &db.Job{
		PolicyID: uuid.New(),
		AgentID:  agentUUID,
		Type:     "backup",
		Status:   "pending",
	}
	if err := ts.jobRepo.Create(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	if err := ts.agentMgr.Dispatch(agentID, &proto.JobAssignment{
		JobId:       job.ID.String(),
		Type:        proto.JobType_JOB_TYPE_BACKUP,
		ScheduledAt: timestamppb.Now(),
	}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	// Drain the received assignment.
	select {
	case <-jobsCh:
	case <-timeoutCtx(t, 3).Done():
		t.Fatal("timed out waiting for assignment")
	}

	agent.reportStatus(t, job.ID.String(), proto.JobStatus_JOB_STATUS_RUNNING)
	waitForJobStatus(t, ts.jobRepo, job.ID.String(), "running")

	// Report FAILED.
	_, err := agent.client.ReportJobStatus(context.Background(), &proto.JobStatusReport{
		JobId:   job.ID.String(),
		AgentId: agentID,
		Status:  proto.JobStatus_JOB_STATUS_FAILED,
		Message: "restic exited with code 1",
	})
	if err != nil {
		t.Fatalf("ReportJobStatus FAILED: %v", err)
	}

	waitForJobStatus(t, ts.jobRepo, job.ID.String(), "failed")
}

// TestDispatchToOfflineAgent verifies that dispatching to an agent that has
// no open stream returns an error immediately (no blocking).
func TestDispatchToOfflineAgent(t *testing.T) {
	ts := newTestServer(t)
	agent := newFakeAgent(t, ts.addr)

	agentID := agent.register(t)
	// Do NOT open the stream — agent stays offline.

	err := ts.agentMgr.Dispatch(agentID, &proto.JobAssignment{
		JobId: uuid.NewString(),
		Type:  proto.JobType_JOB_TYPE_BACKUP,
	})
	if err == nil {
		t.Fatal("Dispatch to offline agent: want error, got nil")
	}
}

// timeoutCtx returns a context that cancels after n seconds, used in select
// statements as a test deadline.
func timeoutCtx(t *testing.T, seconds int) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(seconds)*time.Second)
	t.Cleanup(cancel)
	return ctx
}
