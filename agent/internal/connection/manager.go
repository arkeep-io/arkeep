// Package connection manages the persistent gRPC connection between the agent
// and the server. It handles:
//   - Initial registration (presenting hostname/version, storing the returned agent ID)
//   - Heartbeat loop (periodic liveness signals with system metrics)
//   - StreamJobs loop (receiving job assignments and forwarding to the executor)
//   - StreamLogs (streaming job log lines to the server in real time)
//   - ReportJobStatus (notifying the server of job lifecycle transitions)
//   - Automatic reconnection with exponential backoff + jitter on any failure
//
// The Manager implements executor.LogSink and executor.StatusReporter so the
// executor can call SendLog and ReportStatus without knowing about gRPC.
//
// State persistence: after the first successful registration the server returns
// a stable agent ID (UUIDv7). This ID is written to <state-dir>/agent-state.json
// and reused on every subsequent connection so the server matches the agent to
// the existing record instead of creating a duplicate.
package connection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/arkeep-io/arkeep/agent/internal/executor"
	"github.com/arkeep-io/arkeep/agent/internal/metrics"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

const (
	backoffInitial = 1 * time.Second
	backoffMax     = 60 * time.Second
	backoffFactor  = 2.0
	// jitterFraction adds up to ±20% random jitter to each backoff interval
	// to prevent thundering herd when many agents reconnect simultaneously.
	jitterFraction = 0.2

	// heartbeatInterval is how often the agent sends liveness signals.
	// The server marks an agent offline if no heartbeat arrives within 3x this interval.
	heartbeatInterval = 30 * time.Second
)

// agentState is persisted to disk after the first successful registration.
// It allows the agent to present its stable ID on reconnect so the server
// matches it to the existing record rather than creating a duplicate.
type agentState struct {
	AgentID string `json:"agent_id"`
}

func stateFilePath(stateDir string) string {
	return filepath.Join(stateDir, "agent-state.json")
}

// loadState reads the persisted agent state from disk.
// Returns an empty agentState (AgentID == "") if the file does not exist yet.
func loadState(stateDir string) (agentState, error) {
	data, err := os.ReadFile(stateFilePath(stateDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return agentState{}, nil
		}
		return agentState{}, fmt.Errorf("connection: failed to read state file: %w", err)
	}
	var s agentState
	if err := json.Unmarshal(data, &s); err != nil {
		return agentState{}, fmt.Errorf("connection: corrupted state file: %w", err)
	}
	return s, nil
}

// saveState writes the agent state to disk atomically via temp file + rename.
func saveState(stateDir string, s agentState) error {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("connection: failed to marshal state: %w", err)
	}
	if err := os.MkdirAll(stateDir, 0750); err != nil {
		return fmt.Errorf("connection: failed to create state dir: %w", err)
	}
	tmp, err := os.CreateTemp(stateDir, "agent-state.*.tmp")
	if err != nil {
		return fmt.Errorf("connection: failed to create temp state file: %w", err)
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("connection: failed to write state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("connection: failed to close temp state file: %w", err)
	}
	if err := os.Rename(tmpPath, stateFilePath(stateDir)); err != nil {
		return fmt.Errorf("connection: failed to rename state file: %w", err)
	}
	ok = true
	return nil
}

// Config holds all parameters needed to connect to the server.
type Config struct {
	// ServerAddr is the gRPC server address (e.g. "localhost:9090").
	ServerAddr string
	// SharedSecret is the shared secret sent in gRPC metadata for authentication.
	// Must match the ARKEEP_AGENT_SECRET configured on the server.
	// If empty, no authentication header is sent (server must also have it empty).
	SharedSecret string
	// StateDir is the directory where agent-state.json is persisted.
	StateDir string
	// Version is the agent binary version, sent during registration.
	Version string
	DockerAvailable bool
}

// Manager maintains the persistent gRPC connection to the server.
// It implements executor.LogSink and executor.StatusReporter so the executor
// can forward log lines and status changes without knowing about gRPC.
type Manager struct {
	cfg    Config
	exec   *executor.Executor
	logger *zap.Logger

	// mu protects client and logStreams — both are replaced on every reconnect.
	mu         sync.RWMutex
	client     proto.AgentServiceClient
	logStreams  map[string]proto.AgentService_StreamLogsClient
	// agentID is the stable ID returned by the server after registration.
	// Stored here so SendLog and ReportStatus can include it in RPCs.
	agentID    string
}

// New creates a Manager. Call Run to start the connection loop.
func New(cfg Config, exec *executor.Executor, logger *zap.Logger) *Manager {
	return &Manager{
		cfg:       cfg,
		exec:      exec,
		logger:    logger.Named("connection"),
		logStreams: make(map[string]proto.AgentService_StreamLogsClient),
	}
}

// Run starts the connection loop. It connects to the server, registers, and
// begins the heartbeat and job stream loops. On any error it reconnects with
// exponential backoff. Blocks until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) {
	backoff := backoffInitial

	for {
		if ctx.Err() != nil {
			m.logger.Info("connection manager stopped")
			return
		}

		m.logger.Info("connecting to server", zap.String("addr", m.cfg.ServerAddr))

		if err := m.connect(ctx); err != nil {
			m.logger.Warn("connection failed, retrying",
				zap.Error(err),
				zap.Duration("backoff", backoff),
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(jitter(backoff)):
			}
			backoff = nextBackoff(backoff)
			continue
		}

		// Successful session — reset backoff for the next reconnect.
		backoff = backoffInitial
	}
}

// connect establishes one gRPC session: dial → register → run loops.
// Returns when the session ends (error or context cancellation).
func (m *Manager) connect(ctx context.Context) error {
	conn, err := grpc.NewClient(
		m.cfg.ServerAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("dial failed: %w", err)
	}
	defer conn.Close()

	// Attach the shared secret to every outgoing RPC via metadata.
	// This is equivalent to an HTTP Authorization header — the server's
	// auth interceptor validates it on every call.
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("agent-secret", m.cfg.SharedSecret))

	client := proto.NewAgentServiceClient(conn)
	m.mu.Lock()
	m.client = client
	m.mu.Unlock()

	// --- Register ---
	agentID, agentName, err := m.register(ctx, client)
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	m.mu.Lock()
	m.agentID = agentID
	m.mu.Unlock()

	m.logger.Info("registered with server",
		zap.String("agent_id", agentID),
		zap.String("agent_name", agentName),
	)

	// --- Run heartbeat + job stream concurrently ---
	// Both loops run until one fails, then the entire session is torn down
	// and the outer Run loop reconnects.
	errCh := make(chan error, 2)
	go func() { errCh <- m.heartbeatLoop(ctx, client, agentID) }()
	go func() { errCh <- m.jobStreamLoop(ctx, client, agentID) }()

	err = <-errCh
	if ctx.Err() != nil {
		// Context cancelled (graceful shutdown) — not a real error.
		return nil
	}
	return err
}

// register calls the Register RPC, persists the returned agent ID, and
// returns the ID and display name.
func (m *Manager) register(ctx context.Context, client proto.AgentServiceClient) (string, string, error) {
	state, err := loadState(m.cfg.StateDir)
	if err != nil {
		m.logger.Warn("failed to load agent state, will re-register", zap.Error(err))
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	// AgentCapabilities reflect what is available on this host.
	// The restic and rclone binaries are always present (embedded in the binary),
	// so those are always true. Docker availability is not checked here — the
	// executor handles graceful degradation when Docker is unavailable.
	caps := &proto.AgentCapabilities{
		Restic: true,
		Rclone: true,
		Docker: m.cfg.DockerAvailable,
	}

	resp, err := client.Register(ctx, &proto.RegisterRequest{
		Hostname:     hostname,
		Version:      m.cfg.Version,
		Os:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		Capabilities: caps,
	})
	if err != nil {
		return "", "", fmt.Errorf("Register RPC failed: %w", err)
	}

	// Persist the agent ID if it changed (first run or server reassignment).
	if resp.AgentId != state.AgentID {
		if err := saveState(m.cfg.StateDir, agentState{AgentID: resp.AgentId}); err != nil {
			// Non-fatal: the server deduplicates by hostname so no duplicate
			// record is created on the next restart.
			m.logger.Warn("failed to persist agent state", zap.Error(err))
		}
	}

	return resp.AgentId, resp.AgentName, nil
}

// heartbeatLoop sends periodic Heartbeat RPCs until ctx is cancelled or an
// error occurs. System metrics are collected on each tick and included in the
// request so the server can display resource utilization in the GUI.
func (m *Manager) heartbeatLoop(ctx context.Context, client proto.AgentServiceClient, agentID string) error {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_, err := client.Heartbeat(ctx, &proto.HeartbeatRequest{
				AgentId: agentID,
				Metrics: metrics.Collect(),
			})
			if err != nil {
				return fmt.Errorf("heartbeat failed: %w", err)
			}
			m.logger.Debug("heartbeat sent", zap.String("agent_id", agentID))
		}
	}
}

// jobStreamLoop opens the StreamJobs server-streaming RPC and processes
// incoming job assignments until the stream closes or ctx is cancelled.
func (m *Manager) jobStreamLoop(ctx context.Context, client proto.AgentServiceClient, agentID string) error {
	stream, err := client.StreamJobs(ctx, &proto.StreamJobsRequest{AgentId: agentID})
	if err != nil {
		return fmt.Errorf("StreamJobs open failed: %w", err)
	}

	m.logger.Info("job stream open", zap.String("agent_id", agentID))

	for {
		assignment, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("StreamJobs recv: %w", err)
		}

		job, err := m.protoToJob(assignment)
		if err != nil {
			m.logger.Error("failed to parse job assignment",
				zap.String("job_id", assignment.JobId),
				zap.Error(err),
			)
			continue
		}

		if err := m.exec.Enqueue(job); err != nil {
			m.logger.Error("failed to enqueue job",
				zap.String("job_id", assignment.JobId),
				zap.Error(err),
			)
		}
	}
}

// SendLog implements executor.LogSink. It writes a log entry to the open
// StreamLogs stream for the given job. If no stream is open the line is
// dropped with a warning — this should not happen in normal operation because
// ReportStatus("running") opens the stream before the executor calls SendLog.
func (m *Manager) SendLog(jobID, level, message string) {
	m.mu.RLock()
	stream, ok := m.logStreams[jobID]
	agentID := m.agentID
	m.mu.RUnlock()

	if !ok {
		m.logger.Warn("SendLog: no open log stream for job, dropping line",
			zap.String("job_id", jobID),
		)
		return
	}

	err := stream.Send(&proto.LogEntry{
		JobId:     jobID,
		AgentId:   agentID,
		Level:     levelToProto(level),
		Message:   message,
		Timestamp: timestamppb.Now(),
	})
	if err != nil {
		m.logger.Warn("SendLog: failed to send log entry",
			zap.String("job_id", jobID),
			zap.Error(err),
		)
	}
}

// openLogStream opens a StreamLogs client stream for the given job and
// registers it in logStreams. Called by ReportStatus when status == "running".
func (m *Manager) openLogStream(jobID string) {
	m.mu.RLock()
	client := m.client
	m.mu.RUnlock()

	if client == nil {
		m.logger.Warn("openLogStream: no active client", zap.String("job_id", jobID))
		return
	}

	// Use a background context so the log stream is not tied to any per-job
	// deadline — it must stay open until we explicitly close it.
	stream, err := client.StreamLogs(context.Background())
	if err != nil {
		m.logger.Warn("openLogStream: failed to open stream",
			zap.String("job_id", jobID),
			zap.Error(err),
		)
		return
	}

	m.mu.Lock()
	m.logStreams[jobID] = stream
	m.mu.Unlock()
}

// closeLogStream closes and removes the StreamLogs stream for the given job.
// Called by ReportStatus when status == "success" or "failed".
func (m *Manager) closeLogStream(jobID string) {
	m.mu.Lock()
	stream, ok := m.logStreams[jobID]
	delete(m.logStreams, jobID)
	m.mu.Unlock()

	if !ok {
		return
	}

	if _, err := stream.CloseAndRecv(); err != nil {
		m.logger.Warn("closeLogStream: error closing stream",
			zap.String("job_id", jobID),
			zap.Error(err),
		)
	}
}

// ReportStatus implements executor.StatusReporter. It calls ReportJobStatus
// via gRPC and manages the log stream lifecycle:
//   - "running"          → opens the log stream before reporting
//   - "success"/"failed" → reports status then closes the log stream
func (m *Manager) ReportStatus(jobID, status, message string) {
	if status == "running" {
		m.openLogStream(jobID)
	}

	m.mu.RLock()
	client := m.client
	agentID := m.agentID
	m.mu.RUnlock()

	if client != nil {
		_, err := client.ReportJobStatus(context.Background(), &proto.JobStatusReport{
			JobId:     jobID,
			AgentId:   agentID,
			Status:    statusToProto(status),
			Message:   message,
			Timestamp: timestamppb.Now(),
		})
		if err != nil {
			m.logger.Warn("ReportStatus: RPC failed",
				zap.String("job_id", jobID),
				zap.String("status", status),
				zap.Error(err),
			)
		}
	} else {
		m.logger.Warn("ReportStatus: no active client, status lost",
			zap.String("job_id", jobID),
			zap.String("status", status),
		)
	}

	if status == "success" || status == "failed" {
		m.closeLogStream(jobID)
	}
}

// protoToJob converts a proto.JobAssignment to an executor.JobAssignment.
// The payload bytes are passed through as-is — the executor deserializes them
// according to the job type.
func (m *Manager) protoToJob(p *proto.JobAssignment) (executor.JobAssignment, error) {
	if p.JobId == "" {
		return executor.JobAssignment{}, errors.New("job assignment missing job_id")
	}
	if p.Type != proto.JobType_JOB_TYPE_BACKUP {
		return executor.JobAssignment{}, fmt.Errorf("unsupported job type: %v", p.Type)
	}

	return executor.JobAssignment{
		JobID:    p.JobId,
		PolicyID: p.PolicyId,
		Payload:  p.Payload,
	}, nil
}

// levelToProto converts an internal level string to the proto LogLevel enum.
func levelToProto(level string) proto.LogLevel {
	switch level {
	case "debug":
		return proto.LogLevel_LOG_LEVEL_DEBUG
	case "warn":
		return proto.LogLevel_LOG_LEVEL_WARN
	case "error":
		return proto.LogLevel_LOG_LEVEL_ERROR
	default:
		return proto.LogLevel_LOG_LEVEL_INFO
	}
}

// statusToProto converts an internal status string to the proto JobStatus enum.
func statusToProto(status string) proto.JobStatus {
	switch status {
	case "running":
		return proto.JobStatus_JOB_STATUS_RUNNING
	case "success":
		return proto.JobStatus_JOB_STATUS_COMPLETED
	case "failed":
		return proto.JobStatus_JOB_STATUS_FAILED
	default:
		return proto.JobStatus_JOB_STATUS_UNSPECIFIED
	}
}

// nextBackoff returns the next backoff duration, capped at backoffMax.
func nextBackoff(current time.Duration) time.Duration {
	next := time.Duration(float64(current) * backoffFactor)
	if next > backoffMax {
		return backoffMax
	}
	return next
}

// jitter adds a random ±jitterFraction perturbation to d to avoid
// thundering herd on reconnect.
func jitter(d time.Duration) time.Duration {
	delta := float64(d) * jitterFraction
	offset := (rand.Float64()*2 - 1) * delta
	return time.Duration(float64(d) + offset)
}