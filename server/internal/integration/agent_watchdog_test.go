package integration_test

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/agentwatchdog"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// TestStaleAgentWatchdog_MarksOfflineAndRecoversOrphans is the end-to-end
// regression for issue #234: an agent that stops heartbeating without closing
// its gRPC stream cleanly (network partition, crash, unplugged cable — no FIN
// ever reaches the server) must still be detected and marked offline, with its
// running jobs recovered, exactly as if the stream had closed normally.
func TestStaleAgentWatchdog_MarksOfflineAndRecoversOrphans(t *testing.T) {
	ts := newTestServer(t)
	agent := newFakeAgent(t, ts.addr)

	agentID := agent.register(t)
	_, cancelStream := agent.openStream(t)
	defer cancelStream()
	waitForAgentStatus(t, ts.agentRepo, agentID, "online")

	agentUUID := mustParseUUID(t, agentID)
	if !ts.agentMgr.IsConnected(agentID) {
		t.Fatal("agent should be registered in agentManager after StreamJobs opens")
	}

	// Create a job and drive it to "running", simulating one in flight when the
	// agent went silent.
	job := createIntegrationJob(t, ts, agentUUID)
	agent.reportStatus(t, job.ID.String(), proto.JobStatus_JOB_STATUS_RUNNING)
	waitForJobStatus(t, ts.jobRepo, job.ID.String(), "running")

	// Simulate the passage of time without heartbeats: rewrite last_seen_at
	// into the past instead of waiting out the real staleTimeout.
	stale := time.Now().UTC().Add(-time.Hour)
	if err := ts.agentRepo.UpdateStatus(context.Background(), agentUUID, "online", stale); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	svc := agentwatchdog.NewService(ts.agentRepo, ts.jobRepo, ts.agentMgr, nil, newTestHub(), zap.NewNop())
	flipped := svc.RunOnce(context.Background())

	if flipped != 1 {
		t.Fatalf("RunOnce() = %d, want 1", flipped)
	}

	waitForAgentStatus(t, ts.agentRepo, agentID, "offline")
	waitForJobStatus(t, ts.jobRepo, job.ID.String(), "failed")

	if ts.agentMgr.IsConnected(agentID) {
		t.Error("agent should be deregistered from agentManager after the watchdog marks it offline")
	}
}

// TestStaleAgentWatchdog_SkipsRecentlySeenAgent verifies the watchdog does not
// touch an agent that is genuinely still heartbeating.
func TestStaleAgentWatchdog_SkipsRecentlySeenAgent(t *testing.T) {
	ts := newTestServer(t)
	agent := newFakeAgent(t, ts.addr)

	agentID := agent.register(t)
	_, cancelStream := agent.openStream(t)
	defer cancelStream()
	waitForAgentStatus(t, ts.agentRepo, agentID, "online")

	svc := agentwatchdog.NewService(ts.agentRepo, ts.jobRepo, ts.agentMgr, nil, newTestHub(), zap.NewNop())
	if flipped := svc.RunOnce(context.Background()); flipped != 0 {
		t.Errorf("RunOnce() = %d, want 0 for a recently-seen agent", flipped)
	}

	waitForAgentStatus(t, ts.agentRepo, agentID, "online")
	if !ts.agentMgr.IsConnected(agentID) {
		t.Error("agent should remain registered in agentManager")
	}
}
