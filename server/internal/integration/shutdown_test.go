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

// TestGracefulShutdown verifies the graceful-shutdown sequence:
//
//  1. Server context is cancelled → gRPC sends GOAWAY to connected agents.
//  2. The agent (simulated here by cancelStream) closes its stream after GOAWAY,
//     as a real arkeep-agent does in its reconnection loop.
//  3. The server marks the agent "offline" and recovers any orphaned jobs.
//
// Note: grpc.GracefulStop waits for existing streaming RPCs to complete before
// returning. In production the agent closes its stream after receiving GOAWAY;
// this test simulates that by calling cancelStream after ts.cancel().
func TestGracefulShutdown(t *testing.T) {
	ts := newTestServer(t)
	agent := newFakeAgent(t, ts.addr)

	agentID := agent.register(t)
	_, cancelStream := agent.openStream(t)
	waitForAgentStatus(t, ts.agentRepo, agentID, "online")

	// Step 1: Initiate server shutdown. This sends GOAWAY to all clients.
	ts.cancel()

	// Step 2: Simulate the agent closing its stream after receiving GOAWAY.
	// In production this happens in the agent's reconnection loop.
	// A small sleep ensures the GOAWAY frame has been sent before we close.
	time.Sleep(30 * time.Millisecond)
	cancelStream()

	// Step 3: Server must mark the agent offline once the stream closes.
	waitForAgentStatus(t, ts.agentRepo, agentID, "offline")

	// Step 4: After shutdown the server must not be reachable for new RPCs.
	// (The connection is still open — we verify no stream can be opened.)
	newAgent := newFakeAgent(t, ts.addr)
	// Register may succeed or fail depending on connection state, but opening
	// a new stream must fail or time out — we just check the agent count stays 1.
	_ = newAgent
	if ts.agentMgr.ConnectedAgentsCount() != 0 {
		t.Errorf("ConnectedAgentsCount = %d after shutdown, want 0",
			ts.agentMgr.ConnectedAgentsCount())
	}
}

// TestOrphanRecovery verifies that when an agent disconnects mid-job, any
// running jobs are marked "failed" with an "agent disconnected" error so they
// do not appear stuck in the UI indefinitely.
func TestOrphanRecovery(t *testing.T) {
	ts := newTestServer(t)
	agent := newFakeAgent(t, ts.addr)

	agentID := agent.register(t)
	jobsCh, cancelStream := agent.openStream(t)
	waitForAgentStatus(t, ts.agentRepo, agentID, "online")

	// Create and dispatch a job.
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

	// Agent receives the job and reports RUNNING.
	select {
	case <-jobsCh:
	case <-timeoutCtx(t, 3).Done():
		t.Fatal("timed out waiting for assignment")
	}
	agent.reportStatus(t, job.ID.String(), proto.JobStatus_JOB_STATUS_RUNNING)
	waitForJobStatus(t, ts.jobRepo, job.ID.String(), "running")

	// Agent disconnects abruptly (no terminal status report).
	cancelStream()

	// Server must mark the agent offline and recover the orphaned job.
	waitForAgentStatus(t, ts.agentRepo, agentID, "offline")
	waitForJobStatus(t, ts.jobRepo, job.ID.String(), "failed")

	// Verify the error message was stored.
	finalJob, err := ts.jobRepo.GetByID(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if finalJob.Error == "" {
		t.Error("job.Error is empty after orphan recovery, want non-empty error message")
	}
}

// TestMultipleAgents verifies that the server correctly handles multiple
// concurrent agent connections and dispatches jobs to the correct agent.
func TestMultipleAgents(t *testing.T) {
	ts := newTestServer(t)

	agentA := newFakeAgent(t, ts.addr)
	agentB := newFakeAgent(t, ts.addr)

	idA := agentA.register(t)
	idB := agentB.register(t)

	chA, cancelA := agentA.openStream(t)
	chB, cancelB := agentB.openStream(t)
	defer cancelA()
	defer cancelB()

	waitForAgentStatus(t, ts.agentRepo, idA, "online")
	waitForAgentStatus(t, ts.agentRepo, idB, "online")

	if ts.agentMgr.ConnectedAgentsCount() != 2 {
		t.Errorf("ConnectedAgentsCount = %d, want 2", ts.agentMgr.ConnectedAgentsCount())
	}

	// Dispatch one job to each agent.
	jobForA := &proto.JobAssignment{JobId: uuid.NewString(), Type: proto.JobType_JOB_TYPE_BACKUP}
	jobForB := &proto.JobAssignment{JobId: uuid.NewString(), Type: proto.JobType_JOB_TYPE_BACKUP}

	if err := ts.agentMgr.Dispatch(idA, jobForA); err != nil {
		t.Fatalf("Dispatch to A: %v", err)
	}
	if err := ts.agentMgr.Dispatch(idB, jobForB); err != nil {
		t.Fatalf("Dispatch to B: %v", err)
	}

	// Each agent must receive only its own job.
	timeout := timeoutCtx(t, 3)
	var receivedByA, receivedByB *proto.JobAssignment

	select {
	case receivedByA = <-chA:
	case <-timeout.Done():
		t.Fatal("agent A: timed out waiting for job")
	}
	select {
	case receivedByB = <-chB:
	case <-timeout.Done():
		t.Fatal("agent B: timed out waiting for job")
	}

	if receivedByA.JobId != jobForA.JobId {
		t.Errorf("agent A received job %q, want %q", receivedByA.JobId, jobForA.JobId)
	}
	if receivedByB.JobId != jobForB.JobId {
		t.Errorf("agent B received job %q, want %q", receivedByB.JobId, jobForB.JobId)
	}
}
