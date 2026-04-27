// Package grpc implements the gRPC server that agents connect to.
//
// The server listens on a dedicated port (default: 9090) separate from the
// REST API port (8080). It implements the AgentService defined in
// shared/proto/agent.proto and acts as the bridge between connected agents
// and the rest of the server: it delegates connection lifecycle to
// agentmanager and persistence to AgentRepository.
//
// TLS: when TLSCertFile and TLSKeyFile are set in Config, the gRPC listener
// is wrapped with TLS. In production always provide a certificate — either
// issued by a trusted CA (Let's Encrypt via Caddy/Nginx) or self-signed.
// Agents authenticate via a shared token in gRPC metadata (see authInterceptor).
package grpc

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/metrics"
	"github.com/arkeep-io/arkeep/server/internal/notification"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
	proto "github.com/arkeep-io/arkeep/shared/proto"
	"github.com/google/uuid"
)

// Server is the gRPC server that handles agent connections.
// It wraps the generated UnimplementedAgentServiceServer to ensure
// forward compatibility when new RPCs are added to the proto.
type Server struct {
	proto.UnimplementedAgentServiceServer

	agentManager *agentmanager.Manager
	agentRepo    repositories.AgentRepository
	jobRepo      repositories.JobRepository
	snapshotRepo repositories.SnapshotRepository
	hub          *websocket.Hub
	notifSvc     notification.Service
	metrics      *metrics.Metrics // may be nil when metrics are disabled
	logger       *zap.Logger
	sharedSecret string // shared secret agents must present in gRPC metadata
	tlsCertFile  string
	tlsKeyFile   string
	autoCerts    *AutoCerts // non-nil when auto-PKI + mTLS is active

	// capabilitiesMu guards capabilitiesCache.
	capabilitiesMu sync.Mutex
	// capabilitiesCache stores the AgentCapabilities reported during Register,
	// keyed by agent ID string. StreamJobs reads docker availability from here
	// because StreamJobsRequest does not carry capability fields and the DB
	// model does not persist them.
	capabilitiesCache map[string]*proto.AgentCapabilities
}

// Config holds the configuration for the gRPC server.
type Config struct {
	// SharedSecret is the shared secret agents must send in the "agent-secret"
	// metadata key to authenticate. If empty, a warning is logged and
	// authentication is disabled (development mode only — always set in production).
	SharedSecret string
	// TLSCertFile is the path to the PEM-encoded TLS certificate file.
	// Both TLSCertFile and TLSKeyFile must be set to enable TLS.
	TLSCertFile string
	// TLSKeyFile is the path to the PEM-encoded TLS private key file.
	TLSKeyFile string
	// AutoCerts holds the auto-generated PKI. When set, the gRPC server enables
	// mTLS (RequireAndVerifyClientCert) and the shared-secret token check is
	// bypassed — the client certificate is the authentication proof.
	AutoCerts *AutoCerts
	// NotifService is used to send notifications when jobs complete or agents
	// go offline. Optional — if nil, notifications are silently skipped.
	NotifService notification.Service
	// Metrics is the Prometheus metrics collector. Optional — if nil, no
	// job metrics are recorded.
	Metrics *metrics.Metrics
}

// New creates a new Server instance with the given dependencies.
func New(
	cfg Config,
	agentManager *agentmanager.Manager,
	agentRepo repositories.AgentRepository,
	jobRepo repositories.JobRepository,
	snapshotRepo repositories.SnapshotRepository,
	hub *websocket.Hub,
	logger *zap.Logger,
) *Server {
	return &Server{
		agentManager:      agentManager,
		agentRepo:         agentRepo,
		jobRepo:           jobRepo,
		snapshotRepo:      snapshotRepo,
		hub:               hub,
		notifSvc:          cfg.NotifService,
		metrics:           cfg.Metrics,
		logger:            logger.Named("grpc"),
		sharedSecret:      cfg.SharedSecret,
		tlsCertFile:       cfg.TLSCertFile,
		tlsKeyFile:        cfg.TLSKeyFile,
		autoCerts:         cfg.AutoCerts,
		capabilitiesCache: make(map[string]*proto.AgentCapabilities),
	}
}

// ListenAndServe starts the gRPC server and blocks until the context is
// cancelled or a fatal error occurs. It registers the AgentService and
// attaches the auth interceptor to all incoming RPCs.
//
// The caller is responsible for passing a context that is cancelled on
// shutdown (e.g. via signal handling in cmd/server/main.go).
func (s *Server) ListenAndServe(ctx context.Context, listenAddr string) error {
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("grpc: failed to listen on %s: %w", listenAddr, err)
	}
	return s.Serve(ctx, lis)
}

// Serve starts the gRPC server on an existing listener and blocks until the
// context is cancelled or a fatal error occurs.
//
// This is the lower-level counterpart to ListenAndServe — it accepts a
// pre-created net.Listener so callers (e.g. integration tests) can control
// the bind address and retrieve the actual port before Serve is called.
func (s *Server) Serve(ctx context.Context, lis net.Listener) error {
	const maxMsgSize = 16 * 1024 * 1024 // 16 MB — matches agent client config

	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(s.authUnaryInterceptor),
		grpc.StreamInterceptor(s.authStreamInterceptor),
		grpc.MaxRecvMsgSize(maxMsgSize),
		grpc.MaxSendMsgSize(maxMsgSize),
	}

	switch {
	case s.autoCerts != nil:
		// Auto-PKI: use the generated CA + server cert with mTLS.
		// Client certificates are required and verified against the CA pool —
		// the shared-secret token check is bypassed (see validateToken).
		tlsCfg, err := s.autoCerts.TLSConfig()
		if err != nil {
			return fmt.Errorf("grpc: failed to build mTLS config: %w", err)
		}
		opts = append(opts, grpc.Creds(credentials.NewTLS(tlsCfg)))
		s.logger.Info("gRPC mTLS enabled (auto-PKI)",
			zap.String("ca_cert", s.autoCerts.CACertFile),
			zap.String("addr", lis.Addr().String()),
		)

	case s.tlsCertFile != "" && s.tlsKeyFile != "":
		// Externally managed certificate (e.g. Let's Encrypt via Caddy).
		creds, err := credentials.NewServerTLSFromFile(s.tlsCertFile, s.tlsKeyFile)
		if err != nil {
			return fmt.Errorf("grpc: failed to load TLS credentials: %w", err)
		}
		opts = append(opts, grpc.Creds(creds))
		s.logger.Info("gRPC TLS enabled (external cert)",
			zap.String("cert", s.tlsCertFile),
			zap.String("addr", lis.Addr().String()),
		)

	default:
		s.logger.Warn("gRPC running without TLS (insecure mode) — do not use in production",
			zap.String("addr", lis.Addr().String()),
		)
	}

	grpcServer := grpc.NewServer(opts...)

	proto.RegisterAgentServiceServer(grpcServer, s)

	// Shutdown goroutine: when the context is cancelled (server shutdown),
	// GracefulStop drains in-flight RPCs before closing connections.
	go func() {
		<-ctx.Done()
		s.logger.Info("grpc server shutting down gracefully")
		grpcServer.GracefulStop()
	}()

	s.logger.Info("grpc server listening", zap.String("addr", lis.Addr().String()))

	if err := grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("grpc: server error: %w", err)
	}
	return nil
}

// ─── Auth interceptors ────────────────────────────────────────────────────────

// authUnaryInterceptor validates the agent token on every unary RPC.
func (s *Server) authUnaryInterceptor(
	ctx context.Context,
	req any,
	_ *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	if err := s.validateToken(ctx); err != nil {
		return nil, err
	}
	return handler(ctx, req)
}

// authStreamInterceptor validates the agent token on every streaming RPC.
func (s *Server) authStreamInterceptor(
	srv any,
	ss grpc.ServerStream,
	_ *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	if err := s.validateToken(ss.Context()); err != nil {
		return err
	}
	return handler(srv, ss)
}

// validateToken extracts the "agent-secret" key from gRPC metadata and
// compares it to the configured shared secret.
//
// Metadata in gRPC is the equivalent of HTTP headers — agents set it
// when creating the ClientConn (see agent/internal/connection/manager.go).
func (s *Server) validateToken(ctx context.Context) error {
	// With auto-PKI (mTLS), the client certificate has already been verified
	// by the TLS handshake before any RPC reaches this interceptor.
	// The shared-secret token is not needed — skip the metadata check.
	if s.autoCerts != nil {
		return nil
	}

	// If no secret is configured, auth is disabled (development mode).
	// A warning is logged at startup — see cmd/server/main.go.
	if s.sharedSecret == "" {
		return nil
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing metadata")
	}

	values := md.Get("agent-secret")
	if len(values) == 0 || values[0] != s.sharedSecret {
		return status.Error(codes.Unauthenticated, "invalid agent secret")
	}

	return nil
}

// ─── AgentService implementation ─────────────────────────────────────────────

// Register handles the initial agent registration RPC.
// Upsert logic:
//   - If agent sends a persisted agent_id → look up by ID, update metadata.
//   - Otherwise (first-ever run, or DB wiped) → create a new record.
//
// hostname is stored as display/operational metadata only; it is never used
// as an identity key.
func (s *Server) Register(ctx context.Context, req *proto.RegisterRequest) (*proto.RegisterResponse, error) {
	logger := s.logger.With(zap.String("hostname", req.Hostname))

	// ── Reconnect: agent has a persisted agent_id ─────────────────────────────
	if req.AgentId != "" {
		agentID, err := uuid.Parse(req.AgentId)
		if err != nil {
			logger.Error("register: malformed agent_id",
				zap.String("agent_id", req.AgentId),
				zap.Error(err),
			)
			return nil, status.Error(codes.InvalidArgument, "invalid agent_id")
		}

		existing, err := s.agentRepo.GetByID(ctx, agentID)
		if err != nil && err != repositories.ErrNotFound {
			logger.Error("register: db lookup failed", zap.Error(err))
			return nil, status.Error(codes.Internal, "registration failed")
		}

		if existing != nil {
			existing.Hostname = req.Hostname
			existing.Version = req.Version
			existing.OS = req.Os
			existing.Arch = req.Arch
			existing.DockerAvailable = req.Capabilities != nil && req.Capabilities.Docker

			if err := s.agentRepo.Update(ctx, existing); err != nil {
				logger.Error("register: failed to update agent record", zap.Error(err))
				return nil, status.Error(codes.Internal, "registration failed")
			}

			s.cacheCapabilities(existing.ID.String(), req.Capabilities)

			logger.Info("agent re-registered",
				zap.String("agent_id", existing.ID.String()),
			)
			return &proto.RegisterResponse{
				AgentId:   existing.ID.String(),
				AgentName: existing.Name,
			}, nil
		}

		// agent_id not found (DB was wiped) — fall through to create a new record.
		logger.Warn("register: agent_id not found in DB, creating new record",
			zap.String("agent_id", req.AgentId),
		)
	}

	// ── First-time registration ───────────────────────────────────────────────
	// ID is a UUIDv7 generated in the BeforeCreate hook (see db/models.go).
	// Default display name is the hostname — the user can rename it in the GUI.
	agent := &db.Agent{
		Name:            req.Hostname,
		Hostname:        req.Hostname,
		Version:         req.Version,
		OS:              req.Os,
		Arch:            req.Arch,
		Status:          "offline", // transitions to "online" when StreamJobs opens
		DockerAvailable: req.Capabilities != nil && req.Capabilities.Docker,
	}

	if err := s.agentRepo.Create(ctx, agent); err != nil {
		logger.Error("register: failed to create agent record", zap.Error(err))
		return nil, status.Error(codes.Internal, "registration failed")
	}

	s.cacheCapabilities(agent.ID.String(), req.Capabilities)

	logger.Info("agent registered for the first time",
		zap.String("agent_id", agent.ID.String()),
	)
	return &proto.RegisterResponse{
		AgentId:   agent.ID.String(),
		AgentName: agent.Name,
	}, nil
}

// Heartbeat handles periodic liveness signals from agents.
// It updates the agent's status to "online" and last_seen_at to now,
// then publishes the received system metrics to the WebSocket hub so the
// GUI can display live resource utilization on the agent detail page.
//
// Using UpdateStatus with "online" is intentional: if an agent is sending
// heartbeats it is by definition online, so we can skip a read of the
// current status and update both fields in a single query.
func (s *Server) Heartbeat(ctx context.Context, req *proto.HeartbeatRequest) (*proto.HeartbeatResponse, error) {
	agentID, err := parseAgentID(req.AgentId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid agent_id")
	}

	if err := s.agentRepo.UpdateStatus(ctx, agentID, "online", time.Now().UTC()); err != nil {
		// Non-fatal: log the error but don't fail the heartbeat.
		// A missed update is better than breaking the agent's heartbeat loop.
		s.logger.Warn("failed to update agent status on heartbeat",
			zap.String("agent_id", req.AgentId),
			zap.Error(err),
		)
	}

	// Publish metrics to the WebSocket hub so the GUI detail page can
	// display live CPU, memory and disk utilization.
	// Metrics are optional — older agent versions may not send them.
	if req.Metrics != nil {
		s.hub.Publish("agent:"+req.AgentId, websocket.Message{
			Type: websocket.MsgAgentMetrics,
			Payload: map[string]any{
				"cpu_percent":  req.Metrics.CpuPercent,
				"mem_percent":  req.Metrics.MemPercent,
				"disk_percent": req.Metrics.DiskPercent,
			},
		})
	}

	// has_pending_jobs is always false for now — the scheduler (step 5) will
	// populate this once it is implemented.
	return &proto.HeartbeatResponse{HasPendingJobs: false}, nil
}

// StreamJobs opens the persistent job delivery stream for an agent.
// The agent calls this once after Register and keeps the stream open
// for its entire session. The method blocks until the stream closes
// (agent disconnects or context is cancelled), then cleans up.
func (s *Server) StreamJobs(req *proto.StreamJobsRequest, stream proto.AgentService_StreamJobsServer) error {
	agentID, err := parseAgentID(req.AgentId)
	if err != nil {
		return status.Error(codes.InvalidArgument, "invalid agent_id")
	}

	ctx := stream.Context()

	// Look up the agent to get the hostname for logging and to verify the
	// agent is known before registering its stream in memory.
	agent, err := s.agentRepo.GetByID(ctx, agentID)
	if err != nil {
		s.logger.Error("StreamJobs: agent not found",
			zap.String("agent_id", req.AgentId),
			zap.Error(err),
		)
		return status.Error(codes.NotFound, "agent not found — call Register first")
	}

	// Mark the agent as online in the database.
	if err := s.agentRepo.UpdateStatus(ctx, agentID, "online", time.Now().UTC()); err != nil {
		s.logger.Warn("failed to mark agent online",
			zap.String("agent_id", req.AgentId),
			zap.Error(err),
		)
	}

	// Register the agent in the in-memory manager so the scheduler can
	// dispatch jobs to it by calling manager.Dispatch(agentID, job).
	// Docker availability is read from the capabilities cached during the
	// Register RPC — StreamJobsRequest does not carry capability fields.
	s.agentManager.Register(req.AgentId, agent.Hostname, s.dockerAvailable(req.AgentId), stream)

	// Block until the client disconnects or the server shuts down.
	<-ctx.Done()

	// Cleanup: remove from in-memory registry and mark offline in the DB.
	s.agentManager.Deregister(req.AgentId)

	// Use a fresh context for cleanup since the stream context is already done.
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.agentRepo.UpdateStatus(cleanupCtx, agentID, "offline", time.Now().UTC()); err != nil {
		s.logger.Warn("failed to mark agent offline",
			zap.String("agent_id", req.AgentId),
			zap.Error(err),
		)
	}

	// Orphan recovery: any job still "running" when the agent disconnects will
	// never receive a terminal status report. Mark them as failed so they don't
	// appear stuck in the UI indefinitely. The agent may have already reported
	// "cancelled" for a job it was gracefully shutting down; UpdateStatus is
	// idempotent for terminal states (RowsAffected == 0 if already terminal).
	if n, err := s.jobRepo.FailRunningJobsForAgent(cleanupCtx, agentID, "agent disconnected"); err != nil {
		s.logger.Warn("failed to recover orphaned jobs",
			zap.String("agent_id", req.AgentId),
			zap.Error(err),
		)
	} else if n > 0 {
		s.logger.Info("recovered orphaned jobs",
			zap.String("agent_id", req.AgentId),
			zap.Int64("count", n),
		)
	}

	if s.notifSvc != nil {
		go func() {
			notifCtx, notifCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer notifCancel()
			if err := s.notifSvc.NotifyAgentOffline(notifCtx, agentID, agent.Hostname); err != nil {
				s.logger.Warn("failed to send agent-offline notification", zap.Error(err))
			}
		}()
	}

	s.logger.Info("StreamJobs stream closed", zap.String("agent_id", req.AgentId))
	return nil
}

// ReportJobStatus handles job lifecycle updates from agents.
// It persists the status change to the database so the GUI can display
// real-time job progress.
func (s *Server) ReportJobStatus(ctx context.Context, req *proto.JobStatusReport) (*proto.JobStatusResponse, error) {
	jobID, err := uuid.Parse(req.JobId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid job_id")
	}

	now := time.Now().UTC()

	// dbStatus is the string value stored in the database and expected by the
	// frontend. It is distinct from the proto enum's String() representation
	// (e.g. "succeeded" vs "JOB_STATUS_COMPLETED").
	var dbStatus string
	switch req.Status {
	case proto.JobStatus_JOB_STATUS_RUNNING:
		err = s.jobRepo.UpdateStatus(ctx, jobID, "running", &now, nil, "")
		dbStatus = "running"
	case proto.JobStatus_JOB_STATUS_COMPLETED:
		err = s.jobRepo.UpdateStatus(ctx, jobID, "succeeded", nil, &now, "")
		dbStatus = "succeeded"
	case proto.JobStatus_JOB_STATUS_FAILED:
		err = s.jobRepo.UpdateStatus(ctx, jobID, "failed", nil, &now, req.Message)
		dbStatus = "failed"
	case proto.JobStatus_JOB_STATUS_CANCELLED:
		err = s.jobRepo.UpdateStatus(ctx, jobID, "cancelled", nil, &now, req.Message)
		dbStatus = "cancelled"
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown job status")
	}

	if err != nil {
		s.logger.Error("failed to update job status",
			zap.String("job_id", req.JobId),
			zap.Error(err),
		)
		return nil, status.Error(codes.Internal, "failed to update job status")
	}

	wsPayload := map[string]any{
		"job_id":  req.JobId,
		"status":  dbStatus,
		"message": req.Message,
	}
	// Include finished_at for terminal states so the GUI can update the
	// elapsed-time display without waiting for a full REST fetch.
	if dbStatus == "succeeded" || dbStatus == "failed" || dbStatus == "cancelled" {
		wsPayload["finished_at"] = now.Format(time.RFC3339)
	}
	s.hub.Publish("job:"+req.JobId, websocket.Message{
		Type:    websocket.MsgJobStatus,
		Payload: wsPayload,
	})

	// Fire notifications for terminal job states. Non-fatal: run in a
	// goroutine so a slow notification path never delays the gRPC response.
	if s.notifSvc != nil && (req.Status == proto.JobStatus_JOB_STATUS_COMPLETED || req.Status == proto.JobStatus_JOB_STATUS_FAILED) {
		go s.notifyJobTerminal(jobID, req.Status, req.Message)
	}

	// Record Prometheus metrics for terminal states. Non-fatal: goroutine.
	if s.metrics != nil && dbStatus != "running" {
		go s.recordJobMetrics(jobID, dbStatus)
	}

	s.logger.Info("job status updated",
		zap.String("job_id", req.JobId),
		zap.String("agent_id", req.AgentId),
		zap.String("status", req.Status.String()),
	)

	return &proto.JobStatusResponse{Ok: true}, nil
}

// notifyJobTerminal fetches the job details and fires the appropriate
// notification. Runs in a goroutine — errors are logged, never propagated.
func (s *Server) notifyJobTerminal(jobID uuid.UUID, st proto.JobStatus, errMsg string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	job, _, _, err := s.jobRepo.GetByIDWithDetails(ctx, jobID)
	if err != nil {
		s.logger.Warn("notifyJobTerminal: could not fetch job details",
			zap.String("job_id", jobID.String()),
			zap.Error(err),
		)
		return
	}

	switch st {
	case proto.JobStatus_JOB_STATUS_COMPLETED:
		if err := s.notifSvc.NotifyJobSucceeded(ctx, jobID, job.PolicyID, job.PolicyName); err != nil {
			s.logger.Warn("failed to send job-succeeded notification", zap.Error(err))
		}
	case proto.JobStatus_JOB_STATUS_FAILED:
		if err := s.notifSvc.NotifyJobFailed(ctx, jobID, job.PolicyID, job.PolicyName, errMsg); err != nil {
			s.logger.Warn("failed to send job-failed notification", zap.Error(err))
		}
	}
}

// recordJobMetrics fetches the minimal job fields needed to record Prometheus
// metrics and calls Metrics.RecordJob. Runs in a goroutine — non-fatal.
func (s *Server) recordJobMetrics(jobID uuid.UUID, dbStatus string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	job, err := s.jobRepo.GetByID(ctx, jobID)
	if err != nil {
		s.logger.Warn("recordJobMetrics: could not fetch job",
			zap.String("job_id", jobID.String()),
			zap.Error(err),
		)
		return
	}

	var startedAt, endedAt time.Time
	if job.StartedAt != nil {
		startedAt = *job.StartedAt
	}
	if job.EndedAt != nil {
		endedAt = *job.EndedAt
	}
	s.metrics.RecordJob(dbStatus, job.Type, startedAt, endedAt)
}

// StreamLogs handles the client-streaming RPC for job log ingestion.
// The agent streams log entries in real-time during job execution.
// Entries are flushed to the database in batches of logFlushBatchSize so that
// the GUI can show partial logs for in-progress jobs on page reload.
// Any remaining entries are flushed when the stream closes.
//
// See server/internal/repository/job.go for the BulkCreateLogs implementation.
const logFlushBatchSize = 50

func (s *Server) StreamLogs(stream proto.AgentService_StreamLogsServer) error {
	var (
		entries  []*proto.LogEntry
		jobID    uuid.UUID
		jobIDSet bool
		flushed  int // index into entries up to which we have already persisted
	)

	// flushBatch persists entries[flushed:end] to the DB.
	// Non-fatal: log persistence failure is logged but does not abort the stream.
	flushBatch := func(end int) {
		batch := entries[flushed:end]
		if len(batch) == 0 || !jobIDSet {
			return
		}
		logs := make([]db.JobLog, len(batch))
		for i, e := range batch {
			logs[i] = db.JobLog{
				JobID:   jobID,
				Level:   protoLevelToString(e.Level),
				Message: e.Message,
			}
			if e.Timestamp != nil {
				logs[i].Timestamp = e.Timestamp.AsTime()
			}
		}
		if err := s.jobRepo.BulkCreateLogs(stream.Context(), logs); err != nil {
			s.logger.Error("StreamLogs: failed to persist log batch",
				zap.String("job_id", jobID.String()),
				zap.Int("batch_size", len(logs)),
				zap.Error(err),
			)
		}
		flushed = end
	}

	for {
		entry, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.logger.Warn("StreamLogs: recv error",
				zap.Error(err),
				zap.Int("buffered_entries", len(entries)),
			)
			return status.Errorf(codes.Internal, "recv error: %v", err)
		}

		// Parse the job ID from the first entry; all entries in a stream share
		// the same job ID so we only need to do this once.
		if !jobIDSet {
			parsed, parseErr := uuid.Parse(entry.JobId)
			if parseErr != nil {
				s.logger.Error("StreamLogs: invalid job_id in first log entry",
					zap.String("job_id", entry.JobId),
				)
				return status.Error(codes.InvalidArgument, "invalid job_id in log entries")
			}
			jobID = parsed
			jobIDSet = true
		}

		entries = append(entries, entry)

		// Publish each log line to WebSocket in real-time so the GUI can
		// display a live log tail without waiting for the job to complete.
		// timestamp is included so the frontend can display the correct time
		// even for live entries that have not yet been persisted to the DB.
		ts := time.Now().UTC()
		if entry.Timestamp != nil {
			ts = entry.Timestamp.AsTime().UTC()
		}
		s.hub.Publish("job:"+entry.JobId, websocket.Message{
			Type: websocket.MsgJobLog,
			Payload: map[string]any{
				"job_id":    entry.JobId,
				"level":     protoLevelToString(entry.Level),
				"message":   entry.Message,
				"timestamp": ts.Format(time.RFC3339),
			},
		})

		// Flush to DB every logFlushBatchSize entries so the GUI can show
		// partial logs on page reload without waiting for stream completion.
		if len(entries)-flushed >= logFlushBatchSize {
			flushBatch(len(entries))
		}
	}

	// Flush any remaining entries that did not fill a complete batch.
	flushBatch(len(entries))

	s.logger.Info("StreamLogs completed",
		zap.Int("entries_received", len(entries)),
	)

	return stream.SendAndClose(&proto.LogStreamResponse{
		EntriesReceived: uint32(len(entries)),
	})
}

// ReportDestinationStatus handles per-destination result reports from agents.
// Called once per destination after the backup to that destination completes
// or fails. Persists the restic snapshot ID, byte count, and final status so
// the GUI can show per-destination outcomes on the job detail page.
//
// On success, a Snapshot record is also created so the snapshot appears in
// the snapshots list without requiring a separate restic catalog scan.
func (s *Server) ReportDestinationStatus(ctx context.Context, req *proto.DestinationStatusReport) (*proto.DestinationStatusResponse, error) {
	jobID, err := uuid.Parse(req.JobId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid job_id")
	}

	destID, err := uuid.Parse(req.DestinationId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid destination_id")
	}

	// Find the JobDestination record that matches this job + destination pair.
	// UpdateDestinationStatus identifies the row by JobDestination.ID (not
	// DestinationID), so we need to resolve it first.
	destinations, err := s.jobRepo.ListDestinationsByJob(ctx, jobID)
	if err != nil {
		s.logger.Error("ReportDestinationStatus: failed to list destinations",
			zap.String("job_id", req.JobId),
			zap.Error(err),
		)
		return nil, status.Error(codes.Internal, "failed to look up job destinations")
	}

	var jobDestID uuid.UUID
	for _, d := range destinations {
		if d.DestinationID == destID {
			jobDestID = d.ID
			break
		}
	}

	if jobDestID == (uuid.UUID{}) {
		s.logger.Warn("ReportDestinationStatus: no matching job destination found",
			zap.String("job_id", req.JobId),
			zap.String("destination_id", req.DestinationId),
		)
		return nil, status.Error(codes.NotFound, "job destination not found")
	}

	now := time.Now().UTC()

	// started_at is sent by the agent as the moment restic was invoked for this
	// destination. Fall back to now if the field is absent (older agents).
	startedAt := now
	if req.StartedAt != nil {
		startedAt = req.StartedAt.AsTime().UTC()
	}

	if err := s.jobRepo.UpdateDestinationStatus(ctx, jobDestID, req.Status, &startedAt, &now, req.SnapshotId, req.SizeBytes, req.Error); err != nil {
		s.logger.Error("ReportDestinationStatus: failed to update destination status",
			zap.String("job_id", req.JobId),
			zap.String("destination_id", req.DestinationId),
			zap.Error(err),
		)
		return nil, status.Error(codes.Internal, "failed to update destination status")
	}

	// If the backup to this destination succeeded and the agent reported a
	// restic snapshot ID, persist a Snapshot record. This is the primary way
	// snapshots are created — there is no separate catalog sync step.
	//
	// The job record is fetched to resolve PolicyID, which is not carried in
	// the DestinationStatusReport proto message.
	// Snapshot creation is non-fatal: a failure here does not roll back the
	// destination status update that already succeeded above.
	if req.Status == "succeeded" && req.SnapshotId != "" {
		job, err := s.jobRepo.GetByID(ctx, jobID)
		if err != nil {
			s.logger.Warn("ReportDestinationStatus: could not fetch job for snapshot creation",
				zap.String("job_id", req.JobId),
				zap.Error(err),
			)
		} else {
			snap := &db.Snapshot{
				PolicyID:      job.PolicyID,
				DestinationID: destID,
				JobID:         jobID,
				SnapshotID:    req.SnapshotId,
				SizeBytes:     req.SizeBytes,
				Tags:          "[]",
				SnapshotAt:    now,
			}
			if err := s.snapshotRepo.Create(ctx, snap); err != nil {
				s.logger.Error("ReportDestinationStatus: failed to create snapshot record",
					zap.String("job_id", req.JobId),
					zap.String("snapshot_id", req.SnapshotId),
					zap.Error(err),
				)
			} else {
				s.logger.Info("snapshot record created",
					zap.String("job_id", req.JobId),
					zap.String("destination_id", req.DestinationId),
					zap.String("snapshot_id", req.SnapshotId),
				)
			}
		}
	}

	s.logger.Info("destination status updated",
		zap.String("job_id", req.JobId),
		zap.String("destination_id", req.DestinationId),
		zap.String("status", req.Status),
		zap.String("snapshot_id", req.SnapshotId),
		zap.Int64("size_bytes", req.SizeBytes),
	)

	return &proto.DestinationStatusResponse{Ok: true}, nil
}

// ReportVolumeList receives the Docker volume list from an agent in response
// to a JOB_TYPE_LIST_VOLUMES request sent via StreamJobs. It delivers the
// result to the waiting RequestVolumeList call via the agent manager.
func (s *Server) ReportVolumeList(ctx context.Context, req *proto.VolumeListReport) (*proto.VolumeListResponse, error) {
	s.agentManager.DeliverVolumeList(req)
	return &proto.VolumeListResponse{Ok: true}, nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// parseAgentID parses a string UUID sent by the agent over gRPC into the
// uuid.UUID type used by the repository layer.
func parseAgentID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("invalid agent ID %q: %w", raw, err)
	}
	return id, nil
}

// protoLevelToString converts a proto LogLevel enum to the string values
// accepted by the job_logs_level_check constraint ("info", "warn", "error").
// DEBUG is collapsed to "info" since the DB constraint does not include it.
func protoLevelToString(l proto.LogLevel) string {
	switch l {
	case proto.LogLevel_LOG_LEVEL_WARN:
		return "warn"
	case proto.LogLevel_LOG_LEVEL_ERROR:
		return "error"
	default:
		return "info"
	}
}

// cacheCapabilities stores the agent capabilities reported during Register.
// A nil capabilities pointer is stored as an empty struct so the cache always
// has an entry after a successful registration.
func (s *Server) cacheCapabilities(agentID string, caps *proto.AgentCapabilities) {
	s.capabilitiesMu.Lock()
	defer s.capabilitiesMu.Unlock()
	if caps == nil {
		caps = &proto.AgentCapabilities{}
	}
	s.capabilitiesCache[agentID] = caps
}

// dockerAvailable returns true if the agent advertised Docker support during
// its most recent Register call. Returns false if the agent has not registered
// yet or sent a nil capabilities struct.
func (s *Server) dockerAvailable(agentID string) bool {
	s.capabilitiesMu.Lock()
	defer s.capabilitiesMu.Unlock()
	caps, ok := s.capabilitiesCache[agentID]
	return ok && caps.Docker
}