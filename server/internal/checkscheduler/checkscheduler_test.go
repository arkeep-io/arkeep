package checkscheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/destqueue"
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
	queue    *destqueue.Dispatcher
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
	e := &testEnv{
		dests:    repositories.NewDestinationRepository(gormDB),
		policies: repositories.NewPolicyRepository(gormDB),
		agents:   repositories.NewAgentRepository(gormDB),
		jobs:     repositories.NewJobRepository(gormDB),
		agentMgr: agentmanager.New(zap.NewNop()),
	}
	e.queue = destqueue.New(e.jobs, e.dests, repositories.NewSettingsRepository(gormDB), e.agentMgr, zap.NewNop())
	return e
}

// newScheduler builds a CheckScheduler wired to the env's destination queue,
// registered as its check starter.
func (e *testEnv) newScheduler(t *testing.T) *CheckScheduler {
	t.Helper()
	s, err := New(e.dests, e.jobs, e.agentMgr, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.SetQueue(e.queue)
	e.queue.RegisterStarter("check", s)
	return s
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

// recordingStream is a mockStream that keeps the assignments it was sent.
type recordingStream struct {
	mockStream
	sent []*proto.JobAssignment
}

func (r *recordingStream) Send(a *proto.JobAssignment) error {
	r.sent = append(r.sent, a)
	return nil
}

func TestCheckScheduler_StartStop(t *testing.T) {
	e := newTestEnv(t)
	s := e.newScheduler(t)
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
	agent := e.createAgent(t, "maintenance-agent")
	dest := e.createDestination(t, "d", func(d *db.Destination) {
		d.CheckEnabled = true
		d.RetentionAgentID = &agent.ID
	})

	s := e.newScheduler(t)
	if _, err := s.TriggerNow(context.Background(), dest.ID); err == nil {
		t.Fatal("TriggerNow with an offline maintenance agent returned nil error, want one")
	}

	_, total, err := e.jobs.List(context.Background(), repositories.ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List jobs: %v", err)
	}
	if total != 0 {
		t.Errorf("jobs created while agent offline = %d, want 0", total)
	}
}

func TestTriggerNow_NoMaintenanceAgent_Fails(t *testing.T) {
	e := newTestEnv(t)
	dest := e.createDestination(t, "d", func(d *db.Destination) {
		d.CheckEnabled = true
	})

	s := e.newScheduler(t)
	if _, err := s.TriggerNow(context.Background(), dest.ID); err == nil {
		t.Fatal("TriggerNow with no maintenance agent assigned returned nil error, want one")
	}
}

func TestTriggerNow_NoRepoPassword_Fails(t *testing.T) {
	e := newTestEnv(t)
	agent := e.createAgent(t, "maintenance-agent")
	e.agentMgr.Register(agent.ID.String(), "host", false, &mockStream{})
	dest := e.createDestination(t, "d", func(d *db.Destination) {
		d.CheckEnabled = true
		d.RetentionAgentID = &agent.ID
	})

	s := e.newScheduler(t)
	if _, err := s.TriggerNow(context.Background(), dest.ID); err == nil {
		t.Fatal("TriggerNow with no known repository password returned nil error, want one")
	}
	_, total, err := e.jobs.List(context.Background(), repositories.ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List jobs: %v", err)
	}
	if total != 0 {
		t.Errorf("jobs created without a repository password = %d, want 0", total)
	}
}

func TestTriggerNow_Success(t *testing.T) {
	tests := []struct {
		name       string
		appendOnly bool
	}{
		{"regular destination", false},
		// restic check only reads the repository.
		{"append-only destination", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			e := newTestEnv(t)
			agent := e.createAgent(t, "maintenance-agent")
			stream := &recordingStream{}
			e.agentMgr.Register(agent.ID.String(), "host", false, stream)

			dest := e.createDestination(t, "d", func(d *db.Destination) {
				d.CheckEnabled = true
				d.CheckMode = ModeSubset
				d.CheckSubsetPercent = 10
				d.AppendOnly = tt.appendOnly
				d.RetentionAgentID = &agent.ID
			})
			// A destination created without importing a repository only has
			// the password on its policies.
			policy := &db.Policy{Name: "p", AgentID: agent.ID, Schedule: "@daily", Sources: `[{"type":"directory","path":"/data"}]`, RepoPassword: "policy-pass"}
			if err := e.policies.Create(ctx, policy); err != nil {
				t.Fatalf("create policy: %v", err)
			}
			if err := e.policies.AddDestination(ctx, &db.PolicyDestination{PolicyID: policy.ID, DestinationID: dest.ID}); err != nil {
				t.Fatalf("attach policy: %v", err)
			}

			s := e.newScheduler(t)
			job, err := s.TriggerNow(ctx, dest.ID)
			if err != nil {
				t.Fatalf("TriggerNow: %v", err)
			}
			if job.Type != "check" || job.PolicyID != nil {
				t.Errorf("job = (type %q, policy %v), want (\"check\", nil)", job.Type, job.PolicyID)
			}

			if len(stream.sent) != 1 {
				t.Fatalf("assignments sent = %d, want 1", len(stream.sent))
			}
			a := stream.sent[0]
			if a.Type != proto.JobType_JOB_TYPE_VERIFY || a.JobId != job.ID.String() {
				t.Errorf("assignment = (type %v, job %s), want (VERIFY, %s)", a.Type, a.JobId, job.ID)
			}
			var p checkPayload
			if err := json.Unmarshal(a.Payload, &p); err != nil {
				t.Fatalf("unmarshal payload: %v", err)
			}
			if p.Mode != ModeSubset || p.SubsetPercent != 10 || p.Destination.DestinationID != dest.ID.String() {
				t.Errorf("payload = %+v, want subset 10%% of %s", p, dest.ID)
			}
			if p.RepoPassword != "policy-pass" {
				t.Errorf("payload repo password = %q, want the attached policy's", p.RepoPassword)
			}

			gotDest, err := e.dests.GetByID(ctx, dest.ID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if gotDest.BusyJobID == nil || *gotDest.BusyJobID != job.ID {
				t.Errorf("destination BusyJobID = %v, want %s (busy gate held by the check)", gotDest.BusyJobID, job.ID)
			}
		})
	}
}

func TestTriggerNow_DestinationBusy_Queues(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	agent := e.createAgent(t, "maintenance-agent")
	stream := &recordingStream{}
	e.agentMgr.Register(agent.ID.String(), "host", false, stream)

	dest := e.createDestination(t, "d", func(d *db.Destination) {
		d.CheckEnabled = true
		d.RetentionAgentID = &agent.ID
		d.RepoPassword = "imported-pass"
	})

	// Simulate an in-flight backup already holding the gate.
	holder := &db.Job{AgentID: agent.ID, Type: "backup", Status: "running"}
	if err := e.jobs.Create(ctx, holder); err != nil {
		t.Fatalf("create holder job: %v", err)
	}
	if acquired, err := e.dests.TryAcquireBusy(ctx, dest.ID, holder.ID); err != nil || !acquired {
		t.Fatalf("TryAcquireBusy (holder): acquired=%v err=%v", acquired, err)
	}

	s := e.newScheduler(t)
	job, err := s.TriggerNow(ctx, dest.ID)
	if err != nil {
		t.Fatalf("TriggerNow on a busy destination: %v (want the check queued)", err)
	}
	got, err := e.jobs.GetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != "waiting" {
		t.Errorf("job status = %q, want %q", got.Status, "waiting")
	}
	if len(stream.sent) != 0 {
		t.Errorf("assignments sent while busy = %d, want 0", len(stream.sent))
	}

	// A second trigger must not queue a duplicate check.
	if _, err := s.TriggerNow(ctx, dest.ID); err == nil {
		t.Error("second TriggerNow while a check is queued returned nil error, want one")
	}

	// The holder finishes: the queued check starts.
	if err := e.jobs.UpdateStatus(ctx, holder.ID, "succeeded", nil, nil, ""); err != nil {
		t.Fatalf("finish holder: %v", err)
	}
	e.queue.Drain(ctx)

	got, err = e.jobs.GetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("job status after drain = %q, want %q (dispatched)", got.Status, "pending")
	}
	if len(stream.sent) != 1 {
		t.Errorf("assignments sent after drain = %d, want 1", len(stream.sent))
	}
}

func TestStartQueued_MaintenanceAgentChanged_Fails(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	agent := e.createAgent(t, "maintenance-agent")
	other := e.createAgent(t, "other-agent")
	dest := e.createDestination(t, "d", func(d *db.Destination) {
		d.RetentionAgentID = &other.ID
		d.RepoPassword = "imported-pass"
	})

	job := &db.Job{AgentID: agent.ID, Type: "check", Status: "waiting"}
	if err := e.jobs.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := e.jobs.CreateDestination(ctx, &db.JobDestination{JobID: job.ID, DestinationID: dest.ID, Status: "pending"}); err != nil {
		t.Fatalf("create job destination: %v", err)
	}

	s := e.newScheduler(t)
	admitAll := func(context.Context, []uuid.UUID) (bool, error) { return true, nil }
	if err := s.StartQueued(ctx, job, admitAll); err != nil {
		t.Fatalf("StartQueued: %v", err)
	}
	got, err := e.jobs.GetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != "failed" {
		t.Errorf("job status = %q, want \"failed\"", got.Status)
	}
}
