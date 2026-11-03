package main

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// isolateMetrics gives run a fresh Prometheus registry: run registers its
// collectors on the default one, which panics when run twice in one process.
func isolateMetrics(t *testing.T) {
	t.Helper()
	registerer, gatherer := prometheus.DefaultRegisterer, prometheus.DefaultGatherer
	reg := prometheus.NewRegistry()
	prometheus.DefaultRegisterer, prometheus.DefaultGatherer = reg, reg
	t.Cleanup(func() { prometheus.DefaultRegisterer, prometheus.DefaultGatherer = registerer, gatherer })
}

// TestRun_ListenerFailureIsAnError: when a listener cannot start, run must
// return an error, so the process exits non-zero and a supervisor such as
// systemd (Restart=on-failure) sees a failure rather than a clean stop.
func TestRun_ListenerFailureIsAnError(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = busy.Close() })

	isolateMetrics(t)
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

// TestRun_ShutdownWaitsForBackgroundServices: run must stop the background
// services before closing the database, so none of them queries a closed
// database ("sql: database is closed") while the server shuts down.
func TestRun_ShutdownWaitsForBackgroundServices(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	stderr := os.Stderr
	os.Stderr = w // the logger built by run writes here
	t.Cleanup(func() { os.Stderr = stderr })
	logs := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		logs <- string(b)
	}()

	// A taken gRPC port makes run shut down right after starting, while the
	// background services are running their first sweep.
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = busy.Close() })
	isolateMetrics(t)
	dir := t.TempDir()
	cfg := &config{
		httpAddr:    "127.0.0.1:0",
		grpcAddr:    busy.Addr().String(),
		dbDriver:    "sqlite",
		dbDSN:       filepath.Join(dir, "arkeep.db"),
		dataDir:     filepath.Join(dir, "data"),
		secretKey:   strings.Repeat("ab", 32),
		agentSecret: "test-agent-secret",
		logLevel:    "error",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = run(ctx, cfg)

	// Give a background service that outlived run the time to hit the
	// closed database before the logs are read.
	time.Sleep(500 * time.Millisecond)
	os.Stderr = stderr
	_ = w.Close()
	if out := <-logs; strings.Contains(out, "database is closed") {
		t.Errorf("a background service used the database after it was closed:\n%s", out)
	}
}
