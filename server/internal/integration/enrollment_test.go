package integration_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/api"
	"github.com/arkeep-io/arkeep/server/internal/db"
	grpcserver "github.com/arkeep-io/arkeep/server/internal/grpc"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// TestEnrollment verifies the full enrollment flow:
//
//  1. POST /api/v1/agents/enroll returns a CA cert, client cert, and client key.
//  2. The issued client cert can be used to establish an mTLS gRPC connection.
//  3. The mTLS-authenticated agent can call Register successfully.
func TestEnrollment(t *testing.T) {
	// ── Setup: AutoCerts PKI ──────────────────────────────────────────────────

	dataDir := t.TempDir()
	autoCerts, err := grpcserver.EnsureCerts(dataDir, zap.NewNop())
	if err != nil {
		t.Fatalf("EnsureCerts: %v", err)
	}

	// ── Setup: HTTP enrollment endpoint ──────────────────────────────────────

	enrollHandler := api.NewEnrollHandler(autoCerts, testAgentSecret, zap.NewNop())

	httpSrv := httptest.NewServer(http.HandlerFunc(enrollHandler.Enroll))
	defer httpSrv.Close()

	// ── Step 1: POST /api/v1/agents/enroll ────────────────────────────────────

	body, _ := json.Marshal(map[string]string{"agent_secret": testAgentSecret})
	resp, err := http.Post(httpSrv.URL, "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST enroll: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enroll status = %d, want 200", resp.StatusCode)
	}

	var certs struct {
		CACert     string `json:"ca_cert"`
		ClientCert string `json:"client_cert"`
		ClientKey  string `json:"client_key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&certs); err != nil {
		t.Fatalf("decode enroll response: %v", err)
	}
	if certs.CACert == "" {
		t.Fatal("ca_cert is empty")
	}
	if certs.ClientCert == "" {
		t.Fatal("client_cert is empty")
	}
	if certs.ClientKey == "" {
		t.Fatal("client_key is empty")
	}

	// ── Step 2: Start mTLS gRPC server ────────────────────────────────────────

	gdb, err := db.New(db.Config{
		Driver:   "sqlite",
		DSN:      ":memory:",
		Logger:   zap.NewNop(),
		LogLevel: gormlogger.Silent,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	agentRepo := repositories.NewAgentRepository(gdb)
	agentMgr := agentmanager.New(zap.NewNop())
	hub := websocket.NewHub()

	srv := grpcserver.New(
		grpcserver.Config{AutoCerts: autoCerts},
		agentMgr,
		agentRepo,
		repositories.NewJobRepository(gdb),
		repositories.NewSnapshotRepository(gdb),
		hub,
		zap.NewNop(),
	)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel() })

	go func() { _ = srv.Serve(ctx, lis) }()

	// ── Step 3: Connect via mTLS using the issued certs ───────────────────────

	clientCert, err := tls.X509KeyPair([]byte(certs.ClientCert), []byte(certs.ClientKey))
	if err != nil {
		t.Fatalf("parse client cert/key: %v", err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM([]byte(certs.CACert)) {
		t.Fatal("failed to add CA cert to pool")
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caPool,
		ServerName:   grpcserver.GRPCServerName,
	}

	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
	)
	if err != nil {
		t.Fatalf("dial mTLS: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// ── Step 4: Call Register over mTLS ───────────────────────────────────────

	client := proto.NewAgentServiceClient(conn)
	registerResp, err := client.Register(context.Background(), &proto.RegisterRequest{
		Hostname: "enrolled-agent",
		Version:  "0.0.0-test",
		Os:       "linux",
		Arch:     "amd64",
	})
	if err != nil {
		t.Fatalf("Register over mTLS: %v", err)
	}
	if registerResp.AgentId == "" {
		t.Fatal("Register over mTLS returned empty agent_id")
	}
	if registerResp.AgentName != "enrolled-agent" {
		t.Errorf("agent_name = %q, want enrolled-agent", registerResp.AgentName)
	}
}

// TestEnrollmentWrongSecret verifies that enrollment is rejected when the
// agent presents an incorrect shared secret.
func TestEnrollmentWrongSecret(t *testing.T) {
	dataDir := t.TempDir()
	autoCerts, err := grpcserver.EnsureCerts(dataDir, zap.NewNop())
	if err != nil {
		t.Fatalf("EnsureCerts: %v", err)
	}

	enrollHandler := api.NewEnrollHandler(autoCerts, testAgentSecret, zap.NewNop())
	httpSrv := httptest.NewServer(http.HandlerFunc(enrollHandler.Enroll))
	defer httpSrv.Close()

	body, _ := json.Marshal(map[string]string{"agent_secret": "wrong-secret"})
	resp, err := http.Post(httpSrv.URL, "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST enroll: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 Forbidden", resp.StatusCode)
	}
}
