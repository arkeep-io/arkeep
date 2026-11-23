package repairs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/db/dbtest"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	proto "github.com/arkeep-io/arkeep/shared/proto"
	"github.com/arkeep-io/arkeep/shared/repair"
)

const repoPassword = "repo-password"

// fakeAgents is a Dispatcher whose agents are connected or not, and that
// keeps what it was asked to send.
type fakeAgents struct {
	connected   bool
	dispatchErr error
	sent        []*proto.JobAssignment
}

func (f *fakeAgents) IsConnected(string) bool { return f.connected }

func (f *fakeAgents) Dispatch(_ string, job *proto.JobAssignment) error {
	if f.dispatchErr != nil {
		return f.dispatchErr
	}
	f.sent = append(f.sent, job)
	return nil
}

type env struct {
	svc    *Service
	agents *fakeAgents
	dests  repositories.DestinationRepository
	jobs   repositories.JobRepository
	agent  *db.Agent
	dest   *db.Destination
}

// newEnv builds a destination whose maintenance agent supports repairs and is
// connected, and whose repository password is known.
func newEnv(t *testing.T) *env {
	t.Helper()
	if err := db.InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatal(err)
	}
	gdb := dbtest.New(t)
	e := &env{
		agents: &fakeAgents{connected: true},
		dests:  repositories.NewDestinationRepository(gdb),
		jobs:   repositories.NewJobRepository(gdb),
	}
	agentRepo := repositories.NewAgentRepository(gdb)
	e.agent = &db.Agent{Name: "maintenance", Status: "online", RepairAvailable: true}
	if err := agentRepo.Create(context.Background(), e.agent); err != nil {
		t.Fatal(err)
	}
	e.dest = &db.Destination{Name: "garage", Type: "local", Config: `{"path":"/r"}`, Enabled: true,
		RetentionAgentID: &e.agent.ID, RepoPassword: db.EncryptedString(repoPassword)}
	if err := e.dests.Create(context.Background(), e.dest); err != nil {
		t.Fatal(err)
	}
	e.svc = New(e.dests, agentRepo, e.jobs, e.agents, zap.NewNop())
	return e
}

// claimable reports whether a backup could claim the destination now.
func (e *env) claimable(t *testing.T) bool {
	t.Helper()
	ctx := context.Background()
	job := &db.Job{AgentID: e.agent.ID, Type: "backup", Status: "pending"}
	if err := e.jobs.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	ok, err := e.dests.TryAcquireBusyAll(ctx, []uuid.UUID{e.dest.ID}, job.ID, db.LockShared)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		if err := e.dests.ReleaseBusy(ctx, e.dest.ID, job.ID); err != nil {
			t.Fatal(err)
		}
	}
	return ok
}

func (e *env) repairJobs(t *testing.T) int {
	t.Helper()
	active, err := e.jobs.HasActiveJobOfType(context.Background(), JobType, e.dest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		return 1
	}
	return 0
}

func TestStart_DispatchesUnderMaintenance(t *testing.T) {
	e := newEnv(t)
	p := repair.Params{Action: repair.Snapshots, Apply: true, Forget: true, Hosts: []string{"helios"}}

	started, err := e.svc.Start(context.Background(), e.dest.ID, p, "admin@example.com")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !started.Maintenance {
		t.Error("the repair did not put the destination in maintenance")
	}
	if e.claimable(t) {
		t.Error("a backup could claim the destination during the repair")
	}

	job, err := e.jobs.GetByID(context.Background(), started.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Type != JobType || job.RequestedBy != "admin@example.com" {
		t.Errorf("job = (%q, %q), want a repair requested by the admin", job.Type, job.RequestedBy)
	}
	var recorded repair.Params
	if err := json.Unmarshal([]byte(job.Params), &recorded); err != nil || recorded.Action != repair.Snapshots || !recorded.Forget {
		t.Errorf("job params = %q (%v), want the request", job.Params, err)
	}
	if bytes.Contains([]byte(job.Params), []byte(repoPassword)) {
		t.Error("the repository password was recorded in the job parameters")
	}

	if len(e.agents.sent) != 1 || e.agents.sent[0].Type != proto.JobType_JOB_TYPE_REPAIR || e.agents.sent[0].JobId != job.ID.String() {
		t.Fatalf("sent = %+v, want one repair assignment for the job", e.agents.sent)
	}
	var payload repairPayload
	if err := json.Unmarshal(e.agents.sent[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.RepoPassword != repoPassword || payload.Destination.DestinationID != e.dest.ID.String() || payload.Repair.Action != repair.Snapshots {
		t.Errorf("payload = %+v, want the destination, its password and the request", payload)
	}
}

// TestStart_Refusals: nothing is dispatched, and no job is left behind, when
// the request or the destination's state does not allow a repair.
func TestStart_Refusals(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, e *env)
		params  repair.Params
		wantErr error
	}{
		{name: "invalid request", params: repair.Params{Action: "prune"}, wantErr: ErrInvalidRequest},
		{
			name: "no maintenance agent",
			setup: func(t *testing.T, e *env) {
				e.dest.RetentionAgentID = nil
				if err := e.dests.Update(context.Background(), e.dest); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: ErrNoMaintenanceAgent,
		},
		{name: "agent offline", setup: func(_ *testing.T, e *env) { e.agents.connected = false }, wantErr: ErrAgentOffline},
		{
			name: "destination busy",
			setup: func(t *testing.T, e *env) {
				job := &db.Job{AgentID: e.agent.ID, Type: "backup", Status: "running"}
				if err := e.jobs.Create(context.Background(), job); err != nil {
					t.Fatal(err)
				}
				if ok, err := e.dests.TryAcquireBusyAll(context.Background(), []uuid.UUID{e.dest.ID}, job.ID, db.LockShared); err != nil || !ok {
					t.Fatalf("hold destination: %v %v", ok, err)
				}
			},
			wantErr: repositories.ErrDestinationBusy,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			if tt.setup != nil {
				tt.setup(t, e)
			}
			p := tt.params
			if p.Action == "" {
				p = repair.Params{Action: repair.Index}
			}
			if _, err := e.svc.Start(context.Background(), e.dest.ID, p, "admin@example.com"); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Start = %v, want %v", err, tt.wantErr)
			}
			if len(e.agents.sent) != 0 {
				t.Error("a refused repair was dispatched")
			}
			if e.repairJobs(t) != 0 {
				t.Error("a refused repair left an active job")
			}
		})
	}
}

func TestStart_RefusesAnAgentWithoutRepairSupport(t *testing.T) {
	e := newEnv(t)
	e.agent.RepairAvailable = false
	if err := e.svc.agents.Update(context.Background(), e.agent); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Start(context.Background(), e.dest.ID, repair.Params{Action: repair.Unlock}, "a"); !errors.Is(err, ErrRepairUnsupported) {
		t.Fatalf("Start = %v, want ErrRepairUnsupported", err)
	}
}

func TestStart_OneRepairAtATime(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Start(context.Background(), e.dest.ID, repair.Params{Action: repair.Check}, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Start(context.Background(), e.dest.ID, repair.Params{Action: repair.Unlock}, "a"); !errors.Is(err, repositories.ErrRepairInProgress) {
		t.Fatalf("second Start = %v, want ErrRepairInProgress", err)
	}
	if len(e.agents.sent) != 1 {
		t.Errorf("sent %d assignments, want only the first repair", len(e.agents.sent))
	}
}

// TestStart_DispatchFailureEndsTheMaintenance: a repair the agent never
// received is closed as failed, and the maintenance it started ends with it.
func TestStart_DispatchFailureEndsTheMaintenance(t *testing.T) {
	e := newEnv(t)
	e.agents.dispatchErr = errors.New("stream closed")

	if _, err := e.svc.Start(context.Background(), e.dest.ID, repair.Params{Action: repair.Index}, "a"); err == nil {
		t.Fatal("Start succeeded although the dispatch failed")
	}
	if e.repairJobs(t) != 0 {
		t.Error("the undispatched repair is still active")
	}
	if !e.claimable(t) {
		t.Error("the destination stayed in maintenance after the failed dispatch")
	}
}
