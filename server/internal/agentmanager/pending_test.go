package agentmanager

import (
	"context"
	"testing"
	"time"

	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// startVolumeList registers agent-1 and starts a RequestVolumeList, returning
// the correlation ID the agent was sent and the request's result.
func startVolumeList(t *testing.T, mgr *Manager) (string, <-chan VolumeListResult) {
	t.Helper()
	stream := &captureStream{sent: make(chan *proto.JobAssignment, 1)}
	mgr.Register("agent-1", "host1", false, stream)

	results := make(chan VolumeListResult, 1)
	go func() {
		res, err := mgr.RequestVolumeList(context.Background(), "agent-1", "corr-1")
		if err != nil {
			t.Errorf("RequestVolumeList: %v", err)
		}
		results <- res
	}()

	select {
	case a := <-stream.sent:
		return a.JobId, results
	case <-time.After(time.Second):
		t.Fatal("no volume list assignment was sent to the agent")
		return "", nil
	}
}

// TestDeliverVolumeList_DuplicateDoesNotBlock guards against a repeated report
// blocking the gRPC handler forever on the waiter's one-slot channel.
func TestDeliverVolumeList_DuplicateDoesNotBlock(t *testing.T) {
	mgr := newTestManager()
	corrID, results := startVolumeList(t, mgr)

	report := &proto.VolumeListReport{AgentId: "agent-1", CorrelationId: corrID, Error: "first"}
	done := make(chan struct{})
	go func() {
		mgr.DeliverVolumeList(report)
		mgr.DeliverVolumeList(report)
		mgr.DeliverVolumeList(report)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("DeliverVolumeList blocked on a duplicate report")
	}

	if got := <-results; got.Err != "first" {
		t.Errorf("result Err = %q, want %q", got.Err, "first")
	}
}

// TestDeliverVolumeList_IgnoresOtherAgent guards against an agent answering a
// request that was sent to a different agent.
func TestDeliverVolumeList_IgnoresOtherAgent(t *testing.T) {
	mgr := newTestManager()
	corrID, results := startVolumeList(t, mgr)

	mgr.DeliverVolumeList(&proto.VolumeListReport{AgentId: "agent-2", CorrelationId: corrID, Error: "spoofed"})
	mgr.DeliverVolumeList(&proto.VolumeListReport{AgentId: "agent-1", CorrelationId: corrID, Error: "genuine"})

	if got := <-results; got.Err != "genuine" {
		t.Errorf("result Err = %q, want %q: the report from agent-2 was accepted", got.Err, "genuine")
	}
}

// TestDeliverSnapshotBrowseAndImport_IgnoreOtherAgent checks the same rule on
// the other two request kinds, with a pending entry registered directly.
func TestDeliverSnapshotBrowseAndImport_IgnoreOtherAgent(t *testing.T) {
	mgr := newTestManager()

	browse := make(chan SnapshotBrowseResult, 1)
	imp := make(chan SnapshotImportResult, 1)
	mgr.pendingSnapshotBrowses["b"] = pendingCall[SnapshotBrowseResult]{agentID: "agent-1", ch: browse}
	mgr.pendingSnapshotImports["i"] = pendingCall[SnapshotImportResult]{agentID: "agent-1", ch: imp}

	mgr.DeliverSnapshotBrowse(&proto.SnapshotBrowseReport{AgentId: "agent-2", CorrelationId: "b"})
	mgr.DeliverSnapshotImport(&proto.SnapshotImportReport{AgentId: "agent-2", CorrelationId: "i"})

	if len(browse) != 0 || len(imp) != 0 {
		t.Fatalf("a report from agent-2 was delivered to agent-1's waiter (browse=%d, import=%d)", len(browse), len(imp))
	}

	mgr.DeliverSnapshotBrowse(&proto.SnapshotBrowseReport{AgentId: "agent-1", CorrelationId: "b"})
	mgr.DeliverSnapshotBrowse(&proto.SnapshotBrowseReport{AgentId: "agent-1", CorrelationId: "b"})
	mgr.DeliverSnapshotImport(&proto.SnapshotImportReport{AgentId: "agent-1", CorrelationId: "i"})
	mgr.DeliverSnapshotImport(&proto.SnapshotImportReport{AgentId: "agent-1", CorrelationId: "i"})

	if len(browse) != 1 || len(imp) != 1 {
		t.Errorf("genuine reports delivered: browse=%d, import=%d, want 1 each", len(browse), len(imp))
	}
}
