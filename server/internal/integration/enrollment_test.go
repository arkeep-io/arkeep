package integration_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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
		repositories.NewPolicyRepository(gdb),
		repositories.NewDestinationRepository(gdb),
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

// TestEnrollmentEmptyServerSecret verifies that enrollment fails closed when
// the server has no agent secret configured: it used to issue a client
// certificate to anyone in that case.
func TestEnrollmentEmptyServerSecret(t *testing.T) {
	dataDir := t.TempDir()
	autoCerts, err := grpcserver.EnsureCerts(dataDir, zap.NewNop())
	if err != nil {
		t.Fatalf("EnsureCerts: %v", err)
	}

	enrollHandler := api.NewEnrollHandler(autoCerts, "", zap.NewNop())
	httpSrv := httptest.NewServer(http.HandlerFunc(enrollHandler.Enroll))
	defer httpSrv.Close()

	body, _ := json.Marshal(map[string]string{"agent_secret": ""})
	resp, err := http.Post(httpSrv.URL, "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST enroll: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 Forbidden", resp.StatusCode)
	}
}

// TestEnrollmentWithCSR verifies enrollment with an agent-generated key
// (SEC-26): the response carries no private key, and the certificate issued
// for the CSR works for mTLS with the key the agent kept.
func TestEnrollmentWithCSR(t *testing.T) {
	autoCerts, err := grpcserver.EnsureCerts(t.TempDir(), zap.NewNop())
	if err != nil {
		t.Fatalf("EnsureCerts: %v", err)
	}
	httpSrv := httptest.NewServer(http.HandlerFunc(api.NewEnrollHandler(autoCerts, testAgentSecret, zap.NewNop()).Enroll))
	defer httpSrv.Close()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		t.Fatalf("CreateCertificateRequest: %v", err)
	}
	body, _ := json.Marshal(map[string]string{
		"agent_secret": testAgentSecret,
		"csr":          string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})),
	})
	resp, err := http.Post(httpSrv.URL, "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST enroll: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enroll status = %d, want 200", resp.StatusCode)
	}
	var certs map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&certs); err != nil {
		t.Fatalf("decode enroll response: %v", err)
	}
	if _, ok := certs["client_key"]; ok {
		t.Error("the response carries a client_key: the private key must stay on the agent")
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey: %v", err)
	}
	clientCert, err := tls.X509KeyPair([]byte(certs["client_cert"]), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatalf("the issued certificate does not match the agent's key: %v", err)
	}

	gdb, err := db.New(db.Config{Driver: "sqlite", DSN: ":memory:", Logger: zap.NewNop(), LogLevel: gormlogger.Silent})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	srv := grpcserver.New(
		grpcserver.Config{AutoCerts: autoCerts},
		agentmanager.New(zap.NewNop()),
		repositories.NewAgentRepository(gdb),
		repositories.NewJobRepository(gdb),
		repositories.NewSnapshotRepository(gdb),
		repositories.NewPolicyRepository(gdb),
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

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM([]byte(certs["ca_cert"])) {
		t.Fatal("failed to add CA cert to pool")
	}
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caPool,
		ServerName:   grpcserver.GRPCServerName,
	})))
	if err != nil {
		t.Fatalf("dial mTLS: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := proto.NewAgentServiceClient(conn).Register(context.Background(), &proto.RegisterRequest{Hostname: "csr-agent"}); err != nil {
		t.Fatalf("Register over mTLS with the CSR certificate: %v", err)
	}
}

// TestEnrollmentRejectsBadRequests covers an invalid CSR and an oversized body.
func TestEnrollmentRejectsBadRequests(t *testing.T) {
	autoCerts, err := grpcserver.EnsureCerts(t.TempDir(), zap.NewNop())
	if err != nil {
		t.Fatalf("EnsureCerts: %v", err)
	}
	httpSrv := httptest.NewServer(http.HandlerFunc(api.NewEnrollHandler(autoCerts, testAgentSecret, zap.NewNop()).Enroll))
	defer httpSrv.Close()

	for name, csr := range map[string]string{
		"invalid csr": "-----BEGIN CERTIFICATE REQUEST-----\nAAAA\n-----END CERTIFICATE REQUEST-----\n",
		"oversized":   strings.Repeat("A", 128<<10),
	} {
		body, _ := json.Marshal(map[string]string{"agent_secret": testAgentSecret, "csr": csr})
		resp, err := http.Post(httpSrv.URL, "application/json", bytes.NewReader(body)) //nolint:noctx
		if err != nil {
			t.Fatalf("POST enroll (%s): %v", name, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, resp.StatusCode)
		}
	}
}
