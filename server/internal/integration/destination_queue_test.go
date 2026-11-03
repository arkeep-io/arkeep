package integration_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/db/dbtest"
	"github.com/arkeep-io/arkeep/server/internal/destqueue"
	grpcserver "github.com/arkeep-io/arkeep/server/internal/grpc"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/scheduler"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// TestDestinationQueue_BackupWaitsForSweepThenRunsBesideAnother is the
// destination queue end to end (issues #285, #319): a backup triggered while a
// retention sweep holds the destination waits instead of being skipped, and
// the server sends it to the agent on its own as soon as the sweep reports its
// result. A second backup to the same destination then goes out at once:
// backups share the destination.
func TestDestinationQueue_BackupWaitsForSweepThenRunsBesideAnother(t *testing.T) {
	ctx := context.Background()

	gdb := dbtest.New(t)
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

	// A retention sweep is running on the destination.
	sweep := &db.Job{AgentID: agentID, Type: "retention", Status: "running"}
	if err := jobRepo.Create(ctx, sweep); err != nil {
		t.Fatalf("create sweep: %v", err)
	}
	if err := jobRepo.CreateDestination(ctx, &db.JobDestination{JobID: sweep.ID, DestinationID: dest.ID, Status: "running"}); err != nil {
		t.Fatalf("create sweep destination: %v", err)
	}
	if ok, err := destRepo.TryAcquireBusyAll(ctx, []uuid.UUID{dest.ID}, sweep.ID, db.LockExclusive); err != nil || !ok {
		t.Fatalf("lock destination for the sweep: %v %v", ok, err)
	}

	job1, err := sched.TriggerNow(ctx, first.ID)
	if err != nil {
		t.Fatalf("TriggerNow(first): %v", err)
	}
	waitForJobStatus(t, jobRepo, job1.ID.String(), "waiting")
	select {
	case a := <-received:
		t.Fatalf("agent received job %s while the sweep held the destination", a.JobId)
	case <-time.After(100 * time.Millisecond):
	}

	// The sweep finishes. The release notifies the queue: the backup goes out
	// well before the fallback ticker would fire.
	if _, err := agent.client.ReportDestinationStatus(ctx, &proto.DestinationStatusReport{
		JobId:         sweep.ID.String(),
		AgentId:       agentID.String(),
		DestinationId: dest.ID.String(),
		Status:        "succeeded",
	}); err != nil {
		t.Fatalf("ReportDestinationStatus: %v", err)
	}
	expectAssignment(job1.ID.String())
	waitForJobStatus(t, jobRepo, job1.ID.String(), "pending")

	// A second backup to the same destination runs beside the first.
	job2, err := sched.TriggerNow(ctx, second.ID)
	if err != nil {
		t.Fatalf("TriggerNow(second): %v", err)
	}
	expectAssignment(job2.ID.String())
	locks, err := destRepo.ListLocks(ctx, []uuid.UUID{dest.ID})
	if err != nil {
		t.Fatalf("ListLocks: %v", err)
	}
	if len(locks) != 2 || locks[0].JobID != job1.ID || locks[1].JobID != job2.ID {
		t.Errorf("destination locks = %+v, want both backups", locks)
	}
}
