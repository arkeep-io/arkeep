package agentwatchdog

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
)

type fakeAgentStore struct {
	stale       []db.Agent
	markResults map[uuid.UUID]bool // agent ID -> what MarkOfflineIfStale should return
	markCalls   []uuid.UUID
}

func (f *fakeAgentStore) ListStale(_ context.Context, _ time.Time) ([]db.Agent, error) {
	return f.stale, nil
}

func (f *fakeAgentStore) MarkOfflineIfStale(_ context.Context, id uuid.UUID, _ time.Time) (bool, error) {
	f.markCalls = append(f.markCalls, id)
	return f.markResults[id], nil
}

type fakeJobStore struct {
	failCalls []uuid.UUID
}

func (f *fakeJobStore) FailRunningJobsForAgent(_ context.Context, agentID uuid.UUID, _ string) (int64, error) {
	f.failCalls = append(f.failCalls, agentID)
	return 1, nil
}

type fakeRegistry struct {
	deregisterCalls []string
}

func (f *fakeRegistry) Deregister(agentID string) {
	f.deregisterCalls = append(f.deregisterCalls, agentID)
}

type fakeNotifier struct {
	notifyCalls []uuid.UUID
}

func (f *fakeNotifier) NotifyAgentOffline(_ context.Context, agentID uuid.UUID, _ string) error {
	f.notifyCalls = append(f.notifyCalls, agentID)
	return nil
}

type fakePublisher struct {
	published []websocket.Message
	topics    []string
}

func (f *fakePublisher) Publish(topic string, msg websocket.Message) {
	f.topics = append(f.topics, topic)
	f.published = append(f.published, msg)
}

// TestRunOnce_FlipsStaleAgent verifies that a candidate agent that
// MarkOfflineIfStale confirms is genuinely stale gets the full offline
// cleanup: deregister, orphan job recovery, notification, and a live status
// push — the same sequence StreamJobs runs on a clean disconnect.
func TestRunOnce_FlipsStaleAgent(t *testing.T) {
	agentID := uuid.New()
	agents := &fakeAgentStore{
		stale:       []db.Agent{{SoftDelete: db.SoftDelete{Base: db.Base{ID: agentID}}, Hostname: "remote-pc"}},
		markResults: map[uuid.UUID]bool{agentID: true},
	}
	jobs := &fakeJobStore{}
	registry := &fakeRegistry{}
	notif := &fakeNotifier{}
	pub := &fakePublisher{}

	svc := NewService(agents, jobs, registry, notif, pub, zap.NewNop())
	flipped := svc.RunOnce(context.Background())

	if flipped != 1 {
		t.Errorf("RunOnce() = %d, want 1", flipped)
	}
	if len(registry.deregisterCalls) != 1 || registry.deregisterCalls[0] != agentID.String() {
		t.Errorf("Deregister calls = %v, want [%s]", registry.deregisterCalls, agentID)
	}
	if len(jobs.failCalls) != 1 || jobs.failCalls[0] != agentID {
		t.Errorf("FailRunningJobsForAgent calls = %v, want [%s]", jobs.failCalls, agentID)
	}
	if len(notif.notifyCalls) != 1 || notif.notifyCalls[0] != agentID {
		t.Errorf("NotifyAgentOffline calls = %v, want [%s]", notif.notifyCalls, agentID)
	}
	if len(pub.published) != 1 {
		t.Fatalf("Publish called %d times, want 1", len(pub.published))
	}
	if want := "agent:" + agentID.String(); pub.topics[0] != want {
		t.Errorf("published topic = %q, want %q", pub.topics[0], want)
	}
	if pub.published[0].Type != websocket.MsgAgentStatus {
		t.Errorf("published type = %q, want %q", pub.published[0].Type, websocket.MsgAgentStatus)
	}
	if got := pub.published[0].Payload.(map[string]any)["status"]; got != "offline" {
		t.Errorf("published status = %v, want %q", got, "offline")
	}
}

// TestRunOnce_SkipsAgentThatReconnected is the regression case for the race
// window: MarkOfflineIfStale returning false means the agent heartbeated
// again between ListStale and the write, so none of the offline cleanup must
// run — otherwise a live agent would be deregistered and get a spurious
// offline notification.
func TestRunOnce_SkipsAgentThatReconnected(t *testing.T) {
	agentID := uuid.New()
	agents := &fakeAgentStore{
		stale:       []db.Agent{{SoftDelete: db.SoftDelete{Base: db.Base{ID: agentID}}, Hostname: "remote-pc"}},
		markResults: map[uuid.UUID]bool{agentID: false},
	}
	jobs := &fakeJobStore{}
	registry := &fakeRegistry{}
	notif := &fakeNotifier{}
	pub := &fakePublisher{}

	svc := NewService(agents, jobs, registry, notif, pub, zap.NewNop())
	flipped := svc.RunOnce(context.Background())

	if flipped != 0 {
		t.Errorf("RunOnce() = %d, want 0", flipped)
	}
	if len(registry.deregisterCalls) != 0 {
		t.Errorf("Deregister called %d times, want 0", len(registry.deregisterCalls))
	}
	if len(jobs.failCalls) != 0 {
		t.Errorf("FailRunningJobsForAgent called %d times, want 0", len(jobs.failCalls))
	}
	if len(notif.notifyCalls) != 0 {
		t.Errorf("NotifyAgentOffline called %d times, want 0", len(notif.notifyCalls))
	}
	if len(pub.published) != 0 {
		t.Errorf("Publish called %d times, want 0", len(pub.published))
	}
}

// TestRunOnce_NoStaleAgents verifies the common case — nothing to do — is a
// silent no-op.
func TestRunOnce_NoStaleAgents(t *testing.T) {
	agents := &fakeAgentStore{}
	jobs := &fakeJobStore{}
	registry := &fakeRegistry{}
	notif := &fakeNotifier{}
	pub := &fakePublisher{}

	svc := NewService(agents, jobs, registry, notif, pub, zap.NewNop())
	if flipped := svc.RunOnce(context.Background()); flipped != 0 {
		t.Errorf("RunOnce() = %d, want 0", flipped)
	}
	if len(pub.published) != 0 {
		t.Errorf("Publish called %d times, want 0", len(pub.published))
	}
}

// TestRunOnce_NilNotifierIsSkipped verifies that a nil notifier (matching how
// the gRPC server treats a disabled notification.Service) does not panic and
// simply skips the notification step.
func TestRunOnce_NilNotifierIsSkipped(t *testing.T) {
	agentID := uuid.New()
	agents := &fakeAgentStore{
		stale:       []db.Agent{{SoftDelete: db.SoftDelete{Base: db.Base{ID: agentID}}, Hostname: "remote-pc"}},
		markResults: map[uuid.UUID]bool{agentID: true},
	}
	svc := NewService(agents, &fakeJobStore{}, &fakeRegistry{}, nil, &fakePublisher{}, zap.NewNop())

	if flipped := svc.RunOnce(context.Background()); flipped != 1 {
		t.Errorf("RunOnce() = %d, want 1", flipped)
	}
}
