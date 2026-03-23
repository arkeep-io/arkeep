package agentmanager

import (
	"context"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"

	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// mockStream is a minimal proto.AgentService_StreamJobsServer that satisfies
// the interface for tests that only exercise registration and counting —
// no job dispatching takes place.
type mockStream struct{}

func (m *mockStream) Send(_ *proto.JobAssignment) error { return nil }
func (m *mockStream) SetHeader(_ metadata.MD) error    { return nil }
func (m *mockStream) SendHeader(_ metadata.MD) error   { return nil }
func (m *mockStream) SetTrailer(_ metadata.MD)         {}
func (m *mockStream) Context() context.Context         { return context.Background() }
func (m *mockStream) SendMsg(_ any) error              { return nil }
func (m *mockStream) RecvMsg(_ any) error              { return nil }

func newTestManager() *Manager {
	return New(zap.NewNop())
}

func TestRegister_AddsAgent(t *testing.T) {
	mgr := newTestManager()
	mgr.Register("agent-1", "host1", false, &mockStream{})

	if !mgr.IsConnected("agent-1") {
		t.Error("expected agent-1 to be connected")
	}
	if got := mgr.ConnectedAgentsCount(); got != 1 {
		t.Errorf("ConnectedAgentsCount() = %d, want 1", got)
	}
}

func TestDeregister_RemovesAgent(t *testing.T) {
	mgr := newTestManager()
	mgr.Register("agent-1", "host1", false, &mockStream{})
	mgr.Deregister("agent-1")

	if mgr.IsConnected("agent-1") {
		t.Error("expected agent-1 to be disconnected after Deregister")
	}
	if got := mgr.ConnectedAgentsCount(); got != 0 {
		t.Errorf("ConnectedAgentsCount() = %d, want 0", got)
	}
}

func TestRegister_ReplacesExistingConnection(t *testing.T) {
	mgr := newTestManager()
	mgr.Register("agent-1", "host1", false, &mockStream{})
	mgr.Register("agent-1", "host1", true, &mockStream{})

	if got := mgr.ConnectedAgentsCount(); got != 1 {
		t.Errorf("ConnectedAgentsCount() = %d after duplicate register, want 1", got)
	}
}

func TestConnectedAgents_ReturnsSnapshot(t *testing.T) {
	mgr := newTestManager()
	mgr.Register("agent-1", "host1", false, &mockStream{})
	mgr.Register("agent-2", "host2", true, &mockStream{})

	agents := mgr.ConnectedAgents()
	if len(agents) != 2 {
		t.Fatalf("ConnectedAgents() returned %d agents, want 2", len(agents))
	}

	// Mutating the returned slice must not affect the registry.
	agents[0] = nil
	if got := mgr.ConnectedAgentsCount(); got != 2 {
		t.Errorf("mutating the snapshot changed registry count to %d, want 2", got)
	}
}
