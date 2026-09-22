package retentionscheduler

import (
	"bytes"
	"context"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// mockStream is a minimal proto.AgentService_StreamJobsServer — enough for
// agentmanager.Manager.Register to consider an agent connected and for
// Dispatch to succeed, without a real gRPC connection.
type mockStream struct{}

func (m *mockStream) Send(_ *proto.JobAssignment) error { return nil }
func (m *mockStream) SetHeader(_ metadata.MD) error     { return nil }
func (m *mockStream) SendHeader(_ metadata.MD) error    { return nil }
func (m *mockStream) SetTrailer(_ metadata.MD)          {}
func (m *mockStream) Context() context.Context          { return context.Background() }
func (m *mockStream) SendMsg(_ any) error               { return nil }
func (m *mockStream) RecvMsg(_ any) error               { return nil }

// testEnv bundles everything a test needs: a fresh in-memory DB with all
// migrations applied, real repositories, and an agentmanager.Manager.
type testEnv struct {
	dests    repositories.DestinationRepository
	policies repositories.PolicyRepository
	agents   repositories.AgentRepository
	jobs     repositories.JobRepository
	agentMgr *agentmanager.Manager
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	if err := db.InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("db.InitEncryption: %v", err)
	}
	gormDB, err := db.New(db.Config{
		Driver:   "sqlite",
		DSN:      ":memory:",
		Logger:   zap.NewNop(),
		LogLevel: gormlogger.Silent,
	})
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	return &testEnv{
		dests:    repositories.NewDestinationRepository(gormDB),
		policies: repositories.NewPolicyRepository(gormDB),
		agents:   repositories.NewAgentRepository(gormDB),
		jobs:     repositories.NewJobRepository(gormDB),
		agentMgr: agentmanager.New(zap.NewNop()),
	}
}

func (e *testEnv) createAgent(t *testing.T, name string) *db.Agent {
	t.Helper()
	a := &db.Agent{Name: name, Status: "offline", Labels: "{}"}
	if err := e.agents.Create(context.Background(), a); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return a
}

func (e *testEnv) createDestination(t *testing.T, name string, mutate func(*db.Destination)) *db.Destination {
	t.Helper()
	d := &db.Destination{Name: name, Type: "local", Config: `{"path":"/tmp/r"}`, Enabled: true}
	if mutate != nil {
		mutate(d)
	}
	if err := e.dests.Create(context.Background(), d); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	return d
}

func TestRetentionScheduler_New(t *testing.T) {
	e := newTestEnv(t)
	s, err := New(e.dests, e.jobs, e.agentMgr, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.IsRunning() {
		t.Error("IsRunning() = true before Start")
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !s.IsRunning() {
		t.Error("IsRunning() = false after Start")
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if s.IsRunning() {
		t.Error("IsRunning() = true after Stop")
	}
}

func TestTriggerNow_AgentOffline_NoJobCreated(t *testing.T) {
	e := newTestEnv(t)
	agent := e.createAgent(t, "retention-agent")
	dest := e.createDestination(t, "d", func(d *db.Destination) {
		d.RetentionEnabled = true
		d.RetentionSchedule = "0 3 * * *"
		d.RetentionAgentID = &agent.ID
	})
	policy := &db.Policy{Name: "p", AgentID: agent.ID, Schedule: "@daily", Sources: `[{"type":"directory","path":"/data"}]`}
	if err := e.policies.Create(context.Background(), policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	if err := e.policies.AddDestination(context.Background(), &db.PolicyDestination{PolicyID: policy.ID, DestinationID: dest.ID}); err != nil {
		t.Fatalf("attach policy: %v", err)
	}

	s, err := New(e.dests, e.jobs, e.agentMgr, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Agent never registered — IsConnected is false.
	if _, err := s.TriggerNow(context.Background(), dest.ID); err == nil {
		t.Fatal("TriggerNow with an offline retention agent returned nil error, want one")
	}

	jobs, total, err := e.jobs.List(context.Background(), repositories.ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List jobs: %v", err)
	}
	if total != 0 || len(jobs) != 0 {
		t.Errorf("jobs created while agent offline = %d, want 0 (no queueing — see package doc)", total)
	}
}

func TestTriggerNow_AppendOnly_Fails(t *testing.T) {
	e := newTestEnv(t)
	agent := e.createAgent(t, "retention-agent")
	dest := e.createDestination(t, "d", func(d *db.Destination) {
		d.RetentionEnabled = true
		d.AppendOnly = true
		d.RetentionAgentID = &agent.ID
	})

	s, err := New(e.dests, e.jobs, e.agentMgr, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := s.TriggerNow(context.Background(), dest.ID); err == nil {
		t.Fatal("TriggerNow on an append-only destination returned nil error, want one")
	}
}

func TestTriggerNow_NoRetentionAgent_Fails(t *testing.T) {
	e := newTestEnv(t)
	dest := e.createDestination(t, "d", func(d *db.Destination) {
		d.RetentionEnabled = true
	})

	s, err := New(e.dests, e.jobs, e.agentMgr, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := s.TriggerNow(context.Background(), dest.ID); err == nil {
		t.Fatal("TriggerNow with no retention agent assigned returned nil error, want one")
	}
}

func TestTriggerNow_NoPolicies_Fails(t *testing.T) {
	e := newTestEnv(t)
	agent := e.createAgent(t, "retention-agent")
	dest := e.createDestination(t, "d", func(d *db.Destination) {
		d.RetentionEnabled = true
		d.RetentionAgentID = &agent.ID
	})

	s, err := New(e.dests, e.jobs, e.agentMgr, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := s.TriggerNow(context.Background(), dest.ID); err == nil {
		t.Fatal("TriggerNow on a destination with no attached policies returned nil error, want one")
	}
}

func TestTriggerNow_Success(t *testing.T) {
	e := newTestEnv(t)
	agent := e.createAgent(t, "retention-agent")
	e.agentMgr.Register(agent.ID.String(), "host", false, &mockStream{})

	dest := e.createDestination(t, "d", func(d *db.Destination) {
		d.RetentionEnabled = true
		d.RetentionAgentID = &agent.ID
		d.RetentionDaily = 7
	})
	policy := &db.Policy{Name: "p", AgentID: agent.ID, Schedule: "@daily", Sources: `[{"type":"directory","path":"/data"}]`}
	if err := e.policies.Create(context.Background(), policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	if err := e.policies.AddDestination(context.Background(), &db.PolicyDestination{PolicyID: policy.ID, DestinationID: dest.ID}); err != nil {
		t.Fatalf("attach policy: %v", err)
	}

	s, err := New(e.dests, e.jobs, e.agentMgr, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	job, err := s.TriggerNow(context.Background(), dest.ID)
	if err != nil {
		t.Fatalf("TriggerNow: %v", err)
	}
	if job.Type != "retention" {
		t.Errorf("job.Type = %q, want %q", job.Type, "retention")
	}
	if job.PolicyID != nil {
		t.Errorf("job.PolicyID = %v, want nil", job.PolicyID)
	}

	tags, err := e.jobs.ListRetentionTagsByJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("ListRetentionTagsByJob: %v", err)
	}
	if len(tags) != 1 || tags[0].Tag != "policy:"+policy.ID.String() {
		t.Errorf("retention tags = %+v, want one tag %q", tags, "policy:"+policy.ID.String())
	}

	gotDest, err := e.dests.GetByID(context.Background(), dest.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if gotDest.BusyJobID == nil || *gotDest.BusyJobID != job.ID {
		t.Errorf("destination BusyJobID = %v, want %s (busy gate should be held by the dispatched job)", gotDest.BusyJobID, job.ID)
	}
}

func TestTriggerNow_DestinationBusy_Skips(t *testing.T) {
	e := newTestEnv(t)
	agent := e.createAgent(t, "retention-agent")
	e.agentMgr.Register(agent.ID.String(), "host", false, &mockStream{})

	dest := e.createDestination(t, "d", func(d *db.Destination) {
		d.RetentionEnabled = true
		d.RetentionAgentID = &agent.ID
	})
	policy := &db.Policy{Name: "p", AgentID: agent.ID, Schedule: "@daily", Sources: `[{"type":"directory","path":"/data"}]`}
	if err := e.policies.Create(context.Background(), policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	if err := e.policies.AddDestination(context.Background(), &db.PolicyDestination{PolicyID: policy.ID, DestinationID: dest.ID}); err != nil {
		t.Fatalf("attach policy: %v", err)
	}

	// Simulate an in-flight backup already holding the gate.
	holder := &db.Job{AgentID: agent.ID, Type: "backup", Status: "running"}
	if err := e.jobs.Create(context.Background(), holder); err != nil {
		t.Fatalf("create holder job: %v", err)
	}
	if acquired, err := e.dests.TryAcquireBusy(context.Background(), dest.ID, holder.ID); err != nil || !acquired {
		t.Fatalf("TryAcquireBusy (holder): acquired=%v err=%v", acquired, err)
	}

	s, err := New(e.dests, e.jobs, e.agentMgr, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := s.TriggerNow(context.Background(), dest.ID); err == nil {
		t.Fatal("TriggerNow on a busy destination returned nil error, want one")
	}

	// The gate must still be held by the original holder, not stolen or cleared.
	gotDest, err := e.dests.GetByID(context.Background(), dest.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if gotDest.BusyJobID == nil || *gotDest.BusyJobID != holder.ID {
		t.Errorf("destination BusyJobID = %v, want %s (the original holder)", gotDest.BusyJobID, holder.ID)
	}
}
