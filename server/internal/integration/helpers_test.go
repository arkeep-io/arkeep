// Package integration contains end-to-end tests for the gRPC agent-server
// communication. Tests run entirely in-process using a real SQLite in-memory
// database and a real gRPC server bound to a random loopback port. No real
// restic binary or Docker daemon is required.
//
// Each test gets its own isolated server instance (fresh DB, fresh listener)
// via newTestServer, so tests are fully parallel-safe.
package integration_test

import (
	"bytes"
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
	grpcserver "github.com/arkeep-io/arkeep/server/internal/grpc"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

const testAgentSecret = "integration-test-secret"

// TestMain initialises the AES encryption key required by EncryptedString
// fields before any test in this package runs.
func TestMain(m *testing.M) {
	if err := db.InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		panic("db.InitEncryption: " + err.Error())
	}
	os.Exit(m.Run())
}

// ─── testServer ───────────────────────────────────────────────────────────────

// testServer bundles the live gRPC server with all its dependencies so tests
// can reach both the network endpoint and the underlying repositories directly.
type testServer struct {
	addr      string // "127.0.0.1:<port>" of the live gRPC listener
	agentMgr  *agentmanager.Manager
	agentRepo repositories.AgentRepository
	jobRepo   repositories.JobRepository
	cancel    context.CancelFunc // cancels the server context → graceful stop
}

// newTestServer starts a real gRPC server on a free loopback port backed by a
// fresh SQLite in-memory database. t.Cleanup stops the server when the test ends.
func newTestServer(t *testing.T) *testServer {
	t.Helper()

	gdb, err := db.New(db.Config{
		Driver:   "sqlite",
		DSN:      ":memory:",
		Logger:   zap.NewNop(),
		LogLevel: gormlogger.Silent,
	})
	if err != nil {
		t.Fatalf("newTestServer: open db: %v", err)
	}

	agentRepo := repositories.NewAgentRepository(gdb)
	jobRepo := repositories.NewJobRepository(gdb)
	snapshotRepo := repositories.NewSnapshotRepository(gdb)
	agentMgr := agentmanager.New(zap.NewNop())
	hub := websocket.NewHub()

	srv := grpcserver.New(
		grpcserver.Config{SharedSecret: testAgentSecret},
		agentMgr,
		agentRepo,
		jobRepo,
		snapshotRepo,
		hub,
		zap.NewNop(),
	)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("newTestServer: listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	go func() { _ = srv.Serve(ctx, lis) }()

	ts := &testServer{
		addr:      lis.Addr().String(),
		agentMgr:  agentMgr,
		agentRepo: agentRepo,
		jobRepo:   jobRepo,
		cancel:    cancel,
	}

	t.Cleanup(func() {
		cancel()
		time.Sleep(50 * time.Millisecond) // let GracefulStop drain
	})

	return ts
}

// ─── fakeAgent ────────────────────────────────────────────────────────────────

// fakeAgent is a lightweight gRPC client that simulates an agent connecting to
// the server. It uses the real proto client but has no restic executor — it
// receives jobs and reports status directly for test control.
type fakeAgent struct {
	conn    *grpc.ClientConn
	client  proto.AgentServiceClient
	agentID string // populated after register
}

// newFakeAgent opens a gRPC connection to addr using the shared secret for auth.
// The connection is closed via t.Cleanup.
func newFakeAgent(t *testing.T, addr string) *fakeAgent {
	t.Helper()

	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(agentSecretCred(testAgentSecret)),
	)
	if err != nil {
		t.Fatalf("newFakeAgent: %v", err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	return &fakeAgent{
		conn:   conn,
		client: proto.NewAgentServiceClient(conn),
	}
}

// register calls the Register RPC and stores the returned agent_id.
func (a *fakeAgent) register(t *testing.T) string {
	t.Helper()
	resp, err := a.client.Register(context.Background(), &proto.RegisterRequest{
		Hostname: "integration-test-host",
		Version:  "0.0.0-test",
		Os:       "linux",
		Arch:     "amd64",
	})
	if err != nil {
		t.Fatalf("fakeAgent.register: %v", err)
	}
	a.agentID = resp.AgentId
	return resp.AgentId
}

// openStream opens the StreamJobs server-streaming RPC. Received
// JobAssignments are forwarded to the returned channel. The returned cancel
// function closes the stream from the client side.
func (a *fakeAgent) openStream(t *testing.T) (received <-chan *proto.JobAssignment, cancel context.CancelFunc) {
	t.Helper()
	if a.agentID == "" {
		t.Fatal("fakeAgent.openStream: call register first")
	}

	ctx, cancelFn := context.WithCancel(context.Background())

	stream, err := a.client.StreamJobs(ctx, &proto.StreamJobsRequest{AgentId: a.agentID})
	if err != nil {
		cancelFn()
		t.Fatalf("fakeAgent.openStream: %v", err)
	}

	ch := make(chan *proto.JobAssignment, 8)
	go func() {
		defer close(ch)
		for {
			job, err := stream.Recv()
			if err != nil {
				return
			}
			ch <- job
		}
	}()

	return ch, cancelFn
}

// reportStatus sends a ReportJobStatus RPC.
func (a *fakeAgent) reportStatus(t *testing.T, jobID string, st proto.JobStatus) {
	t.Helper()
	_, err := a.client.ReportJobStatus(context.Background(), &proto.JobStatusReport{
		JobId:   jobID,
		AgentId: a.agentID,
		Status:  st,
	})
	if err != nil {
		t.Fatalf("fakeAgent.reportStatus(%v): %v", st, err)
	}
}

// ─── Per-RPC credentials ──────────────────────────────────────────────────────

// agentSecretCred injects the "agent-secret" metadata key on every RPC,
// matching the server's authUnaryInterceptor / authStreamInterceptor check.
type agentSecretCred string

func (c agentSecretCred) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"agent-secret": string(c)}, nil
}

func (c agentSecretCred) RequireTransportSecurity() bool { return false }

// ─── Polling helpers ──────────────────────────────────────────────────────────

// pollUntil retries fn every 20 ms until it returns true or the timeout expires.
func pollUntil(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pollUntil: condition not met within %s", timeout)
}

// waitForAgentStatus polls agentRepo until agent.Status == want.
func waitForAgentStatus(t *testing.T, repo repositories.AgentRepository, agentID, want string) {
	t.Helper()
	id := mustParseUUID(t, agentID)
	pollUntil(t, 3*time.Second, func() bool {
		a, err := repo.GetByID(context.Background(), id)
		return err == nil && a.Status == want
	})
}

// waitForJobStatus polls jobRepo until job.Status == want.
func waitForJobStatus(t *testing.T, repo repositories.JobRepository, jobID, want string) {
	t.Helper()
	id := mustParseUUID(t, jobID)
	pollUntil(t, 3*time.Second, func() bool {
		j, err := repo.GetByID(context.Background(), id)
		return err == nil && j.Status == want
	})
}

func mustParseUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("mustParseUUID(%q): %v", s, err)
	}
	return id
}
