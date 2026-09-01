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
	session := mgr.Register("agent-1", "host1", false, &mockStream{})

	if !mgr.Deregister("agent-1", session) {
		t.Error("Deregister returned false for the current session, want true")
	}
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

// TestDeregister_IgnoresSupersededSession covers the laptop-wake ordering: the
// agent has already reconnected when the previous stream's context finally
// expires. That late teardown must not remove the live connection.
func TestDeregister_IgnoresSupersededSession(t *testing.T) {
	mgr := newTestManager()
	stale := mgr.Register("agent-1", "host1", false, &mockStream{})
	live := mgr.Register("agent-1", "host1", false, &mockStream{})

	if stale == live {
		t.Fatalf("Register handed out the same token twice (%d): sessions cannot be told apart", stale)
	}
	if mgr.Deregister("agent-1", stale) {
		t.Error("Deregister returned true for a superseded session, want false")
	}
	if !mgr.IsConnected("agent-1") {
		t.Error("the live agent was removed by the teardown of a superseded session")
	}

	// The live session can still deregister itself.
	if !mgr.Deregister("agent-1", live) {
		t.Error("Deregister returned false for the live session, want true")
	}
	if mgr.IsConnected("agent-1") {
		t.Error("expected agent-1 to be disconnected after the live session tore down")
	}
}

// TestDeregister_UnknownAgent guards the branch where nothing is registered.
func TestDeregister_UnknownAgent(t *testing.T) {
	mgr := newTestManager()
	if mgr.Deregister("agent-does-not-exist", 1) {
		t.Error("Deregister returned true for an unregistered agent, want false")
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
