package integration_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
	grpcserver "github.com/arkeep-io/arkeep/server/internal/grpc"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// mtlsServer is a gRPC server with auto-PKI, the mode in which every agent has
// its own client certificate and is bound to it (SEC-24).
type mtlsServer struct {
	addr      string
	autoCerts *grpcserver.AutoCerts
	agents    repositories.AgentRepository
	jobs      repositories.JobRepository
	policies  repositories.PolicyRepository
}

func newMTLSServer(t *testing.T) *mtlsServer {
	t.Helper()
	autoCerts, err := grpcserver.EnsureCerts(t.TempDir(), zap.NewNop())
	if err != nil {
		t.Fatalf("EnsureCerts: %v", err)
	}
	gdb, err := db.New(db.Config{Driver: "sqlite", DSN: ":memory:", Logger: zap.NewNop(), LogLevel: gormlogger.Silent})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	ms := &mtlsServer{
		autoCerts: autoCerts,
		agents:    repositories.NewAgentRepository(gdb),
		jobs:      repositories.NewJobRepository(gdb),
		policies:  repositories.NewPolicyRepository(gdb),
	}
	srv := grpcserver.New(
		grpcserver.Config{AutoCerts: autoCerts},
		agentmanager.New(zap.NewNop()),
		ms.agents,
		ms.jobs,
		repositories.NewSnapshotRepository(gdb),
		ms.policies,
		repositories.NewDestinationRepository(gdb),
		websocket.NewHub(),
		zap.NewNop(),
	)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Serve(ctx, lis) }()
	ms.addr = lis.Addr().String()
	return ms
}

// client enrolls a new certificate and returns a client connected with it.
func (ms *mtlsServer) client(t *testing.T) proto.AgentServiceClient {
	t.Helper()
	certPEM, keyPEM, err := ms.autoCerts.IssueCertificate("arkeep-agent-" + uuid.NewString())
	if err != nil {
		t.Fatalf("IssueCertificate: %v", err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ms.autoCerts.CACertPEM)
	conn, err := grpc.NewClient(ms.addr, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   grpcserver.GRPCServerName,
	})))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return proto.NewAgentServiceClient(conn)
}

func register(t *testing.T, c proto.AgentServiceClient, agentID string) (string, error) {
	t.Helper()
	resp, err := c.Register(context.Background(), &proto.RegisterRequest{Hostname: "host", AgentId: agentID})
	if err != nil {
		return "", err
	}
	return resp.AgentId, nil
}

func heartbeat(c proto.AgentServiceClient, agentID string) error {
	_, err := c.Heartbeat(context.Background(), &proto.HeartbeatRequest{AgentId: agentID})
	return err
}

func wantCode(t *testing.T, what string, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Errorf("%s: code = %v (%v), want %v", what, got, err, want)
	}
}

// TestAgentIdentity_CertificateIsBoundOnRegister: once an agent registered
// with its certificate, no other certificate can act as it.
func TestAgentIdentity_CertificateIsBoundOnRegister(t *testing.T) {
	ms := newMTLSServer(t)
	a, b := ms.client(t), ms.client(t)

	idA, err := register(t, a, "")
	if err != nil {
		t.Fatalf("register A: %v", err)
	}
	if err := heartbeat(a, idA); err != nil {
		t.Errorf("A heartbeat as itself: %v", err)
	}

	wantCode(t, "B heartbeat as A", heartbeat(b, idA), codes.PermissionDenied)
	_, err = register(t, b, idA)
	wantCode(t, "B register as A", err, codes.PermissionDenied)

	idB, err := register(t, b, "")
	if err != nil {
		t.Fatalf("register B as a new agent: %v", err)
	}
	if idB == idA {
		t.Fatal("B was given A's identity")
	}

	// A's certificate is bound to A: it cannot become a second agent.
	_, err = register(t, a, "")
	wantCode(t, "A register as a new agent", err, codes.PermissionDenied)
}

// TestAgentIdentity_TrustOnFirstUse covers agents registered before the
// upgrade: the first certificate that registers as one binds it.
func TestAgentIdentity_TrustOnFirstUse(t *testing.T) {
	ms := newMTLSServer(t)
	ctx := context.Background()
	legacy := &db.Agent{Name: "legacy", Hostname: "legacy", Status: "offline"}
	if err := ms.agents.Create(ctx, legacy); err != nil {
		t.Fatalf("create legacy agent: %v", err)
	}
	a, b := ms.client(t), ms.client(t)

	// An unbound agent accepts no RPC but Register.
	wantCode(t, "heartbeat before register", heartbeat(a, legacy.ID.String()), codes.PermissionDenied)

	if _, err := register(t, a, legacy.ID.String()); err != nil {
		t.Fatalf("A register as the legacy agent: %v", err)
	}
	if err := heartbeat(a, legacy.ID.String()); err != nil {
		t.Errorf("A heartbeat after binding: %v", err)
	}
	_, err := register(t, b, legacy.ID.String())
	wantCode(t, "B register as the bound legacy agent", err, codes.PermissionDenied)

	// A second unbound agent cannot be claimed by A's certificate.
	other := &db.Agent{Name: "other", Hostname: "other", Status: "offline"}
	if err := ms.agents.Create(ctx, other); err != nil {
		t.Fatalf("create other agent: %v", err)
	}
	_, err = register(t, a, other.ID.String())
	wantCode(t, "A register as a second agent", err, codes.PermissionDenied)

	// An admin reset lets a new certificate bind the agent.
	if err := ms.agents.ResetCertFingerprint(ctx, legacy.ID); err != nil {
		t.Fatalf("ResetCertFingerprint: %v", err)
	}
	if _, err := register(t, b, legacy.ID.String()); err != nil {
		t.Fatalf("B register after reset: %v", err)
	}
	wantCode(t, "A heartbeat after the reset", heartbeat(a, legacy.ID.String()), codes.PermissionDenied)
}

// TestAgentIdentity_ReportsOnlyOwnJobs: an agent cannot report on a job given
// to another agent (SEC-28).
func TestAgentIdentity_ReportsOnlyOwnJobs(t *testing.T) {
	ms := newMTLSServer(t)
	ctx := context.Background()
	a, b := ms.client(t), ms.client(t)
	idA, err := register(t, a, "")
	if err != nil {
		t.Fatalf("register A: %v", err)
	}
	idB, err := register(t, b, "")
	if err != nil {
		t.Fatalf("register B: %v", err)
	}

	agentA := uuid.MustParse(idA)
	policy := &db.Policy{Name: "p", AgentID: agentA, Schedule: "@daily", Sources: `["/data"]`, RepoPassword: db.EncryptedString("x")}
	if err := ms.policies.Create(ctx, policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	job := &db.Job{PolicyID: &policy.ID, AgentID: agentA, Type: "backup", Status: "pending"}
	if err := ms.jobs.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	report := func(c proto.AgentServiceClient, agentID string) error {
		_, err := c.ReportJobStatus(ctx, &proto.JobStatusReport{JobId: job.ID.String(), AgentId: agentID, Status: proto.JobStatus_JOB_STATUS_RUNNING})
		return err
	}
	wantCode(t, "B reports on A's job as itself", report(b, idB), codes.PermissionDenied)
	wantCode(t, "B reports on A's job as A", report(b, idA), codes.PermissionDenied)
	if err := report(a, idA); err != nil {
		t.Errorf("A reports on its own job: %v", err)
	}

	stored, err := ms.jobs.GetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.Status != "running" {
		t.Errorf("job status = %q, want \"running\" (only A's report applied)", stored.Status)
	}
}
