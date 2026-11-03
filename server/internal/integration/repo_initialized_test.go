package integration_test

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

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

// TestRepoMustExist_AfterFirstSuccess is the fix for a backup silently
// recreating a missing repository (an unmounted disk): the first backup to a
// destination may create its repository, but once a result for it has
// succeeded, every later backup tells the agent the repository must already
// exist. A failed result does not count.
func TestRepoMustExist_AfterFirstSuccess(t *testing.T) {
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

	dest := &db.Destination{Name: "usb", Type: "local", Credentials: db.EncryptedString(`{}`), Config: `{"path":"/mnt/usb"}`, Enabled: true}
	if err := destRepo.Create(ctx, dest); err != nil {
		t.Fatalf("create destination: %v", err)
	}

	// backup triggers a fresh policy on dest (a policy has one backup at a
	// time) and returns the job the agent receives and whether its payload
	// says the repository must exist.
	backup := func(name string) (jobID string, mustExist bool) {
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
		if _, err := sched.TriggerNow(ctx, p.ID); err != nil {
			t.Fatalf("TriggerNow(%s): %v", name, err)
		}
		select {
		case a := <-received:
			var payload struct {
				Destinations []struct {
					RepoMustExist bool `json:"repo_must_exist"`
				} `json:"destinations"`
			}
			if err := json.Unmarshal(a.Payload, &payload); err != nil || len(payload.Destinations) != 1 {
				t.Fatalf("payload of %s: %v (%s)", name, err, a.Payload)
			}
			return a.JobId, payload.Destinations[0].RepoMustExist
		case <-time.After(3 * time.Second):
			t.Fatalf("agent did not receive the %s backup", name)
		}
		return "", false
	}
	report := func(jobID, status string) {
		t.Helper()
		if _, err := agent.client.ReportDestinationStatus(ctx, &proto.DestinationStatusReport{
			JobId:         jobID,
			AgentId:       agentID.String(),
			DestinationId: dest.ID.String(),
			Status:        status,
		}); err != nil {
			t.Fatalf("ReportDestinationStatus(%s): %v", status, err)
		}
	}

	job, mustExist := backup("first")
	if mustExist {
		t.Fatal("first backup: repo_must_exist = true, want the agent free to create the repository")
	}
	report(job, "failed")

	job, mustExist = backup("after a failure")
	if mustExist {
		t.Fatal("backup after a failed one: repo_must_exist = true, want false")
	}
	report(job, "succeeded")

	if _, mustExist = backup("after a success"); !mustExist {
		t.Fatal("backup after a successful one: repo_must_exist = false, want true")
	}
}
