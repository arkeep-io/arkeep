package integration_test

import (
	"context"
	"net"
	"testing"
	"time"

	"go.uber.org/zap"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/destqueue"
	grpcserver "github.com/arkeep-io/arkeep/server/internal/grpc"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/scheduler"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// TestDestinationQueue_SecondBackupStartsWhenFirstFinishes is the scenario of
// issue #285 end to end: two policies back up to the same destination at the
// same time. The second waits instead of being skipped, and the server sends
// it to the agent on its own as soon as the first one reports its result.
func TestDestinationQueue_SecondBackupStartsWhenFirstFinishes(t *testing.T) {
	ctx := context.Background()

	gdb, err := db.New(db.Config{Driver: "sqlite", DSN: ":memory:", Logger: zap.NewNop(), LogLevel: gormlogger.Silent})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	agentRepo := repositories.NewAgentRepository(gdb)
	jobRepo := repositories.NewJobRepository(gdb)
	policyRepo := repositories.NewPolicyRepository(gdb)
	destRepo := repositories.NewDestinationRepository(gdb)
	agentMgr := agentmanager.New(zap.NewNop())

	queue := destqueue.New(jobRepo, destRepo, repositories.NewSettingsRepository(gdb), agentMgr, zap.NewNop())
	sched, err := scheduler.New(policyRepo, jobRepo, destRepo, agentMgr, zap.NewNop())
	if err != nil {
		t.Fatalf("scheduler.New: %v", err)
	}
	sched.SetQueue(queue)
	queue.RegisterStarter("backup", sched)

	srv := grpcserver.New(
		grpcserver.Config{SharedSecret: testAgentSecret, PendingDispatch: sched, Queue: queue},
		agentMgr, agentRepo, jobRepo, repositories.NewSnapshotRepository(gdb), policyRepo, destRepo,
		websocket.NewHub(), zap.NewNop(),
	)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srvCtx, cancel := context.WithCancel(ctx)
	go func() { _ = srv.Serve(srvCtx, lis) }()
	go queue.Run(srvCtx)
	t.Cleanup(func() {
		cancel()
		time.Sleep(50 * time.Millisecond)
	})

	agent := newFakeAgent(t, lis.Addr().String())
	agentID := mustParseUUID(t, agent.register(t))
	received, closeStream := agent.openStream(t)
	defer closeStream()
	pollUntil(t, 3*time.Second, func() bool { return agentMgr.IsConnected(agentID.String()) })

	dest := &db.Destination{Name: "shared", Type: "local", Credentials: db.EncryptedString(`{}`), Config: `{"path":"/tmp/repo"}`, Enabled: true}
	if err := destRepo.Create(ctx, dest); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	newPolicy := func(name string) *db.Policy {
		t.Helper()
		p := &db.Policy{
			Name:         name,
			AgentID:      agentID,
			Schedule:     "0 */3 * * *",
			Enabled:      true,
			Sources:      `[{"type":"directory","path":"/data"}]`,
			RepoPassword: db.EncryptedString("secret"),
		}
		if err := policyRepo.Create(ctx, p); err != nil {
			t.Fatalf("create policy: %v", err)
		}
		if err := policyRepo.AddDestination(ctx, &db.PolicyDestination{PolicyID: p.ID, DestinationID: dest.ID}); err != nil {
			t.Fatalf("attach destination: %v", err)
		}
		return p
	}
	first := newPolicy("first")
	second := newPolicy("second")

	expectAssignment := func(wantJobID string) {
		t.Helper()
		select {
		case a := <-received:
			if a.JobId != wantJobID || a.Type != proto.JobType_JOB_TYPE_BACKUP {
				t.Fatalf("agent received job %s (%v), want backup %s", a.JobId, a.Type, wantJobID)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("agent did not receive job %s", wantJobID)
		}
	}

	job1, err := sched.TriggerNow(ctx, first.ID)
	if err != nil {
		t.Fatalf("TriggerNow(first): %v", err)
	}
	expectAssignment(job1.ID.String())

	job2, err := sched.TriggerNow(ctx, second.ID)
	if err != nil {
		t.Fatalf("TriggerNow(second): %v", err)
	}
	waitForJobStatus(t, jobRepo, job2.ID.String(), "waiting")
	select {
	case a := <-received:
		t.Fatalf("agent received job %s while the destination was busy", a.JobId)
	case <-time.After(100 * time.Millisecond):
	}

	// The first backup runs and finishes.
	agent.reportStatus(t, job1.ID.String(), proto.JobStatus_JOB_STATUS_RUNNING)
	if _, err := agent.client.ReportDestinationStatus(ctx, &proto.DestinationStatusReport{
		JobId:         job1.ID.String(),
		AgentId:       agentID.String(),
		DestinationId: dest.ID.String(),
		Status:        "succeeded",
	}); err != nil {
		t.Fatalf("ReportDestinationStatus: %v", err)
	}

	// The release notifies the queue: the second backup goes out well before
	// the fallback ticker would fire.
	expectAssignment(job2.ID.String())
	waitForJobStatus(t, jobRepo, job2.ID.String(), "pending")
	pollUntil(t, 3*time.Second, func() bool {
		d, err := destRepo.GetByID(ctx, dest.ID)
		return err == nil && d.BusyJobID != nil && *d.BusyJobID == job2.ID
	})
}
