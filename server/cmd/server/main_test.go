package main

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRun_ListenerFailureIsAnError: when a listener cannot start, run must
// return an error, so the process exits non-zero and a supervisor such as
// systemd (Restart=on-failure) sees a failure rather than a clean stop.
func TestRun_ListenerFailureIsAnError(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = busy.Close() })

	dir := t.TempDir()
	cfg := &config{
		httpAddr:    "127.0.0.1:0",
		grpcAddr:    busy.Addr().String(), // already taken
		dbDriver:    "sqlite",
		dbDSN:       filepath.Join(dir, "arkeep.db"),
		dataDir:     filepath.Join(dir, "data"),
		secretKey:   strings.Repeat("ab", 32),
		agentSecret: "test-agent-secret",
		logLevel:    "error",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = run(ctx, cfg)
	if ctx.Err() != nil {
		t.Fatal("run did not stop after the gRPC listener failed")
	}
	if err == nil || !strings.Contains(err.Error(), "grpc") {
		t.Errorf("run() = %v, want the gRPC listener error", err)
	}
}
