// Package grpc implements the gRPC server that agents connect to.
//
// The server listens on a dedicated port (default: 9090) separate from the
// REST API port (8080). It implements the AgentService defined in
// shared/proto/agent.proto and acts as the bridge between connected agents
// and the rest of the server: it delegates connection lifecycle to
// agentmanager and persistence to AgentRepository.
//
// Security note: in production, the gRPC listener should be wrapped with
// TLS. For the initial release, mutual TLS between server and agent is on
// the roadmap. Currently, agents authenticate via a shared token passed
// in gRPC metadata (see authInterceptor).
package grpc

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/google/uuid"
	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// Server is the gRPC server that handles agent connections.
// It wraps the generated UnimplementedAgentServiceServer to ensure
// forward compatibility when new RPCs are added to the proto.
type Server struct {
	proto.UnimplementedAgentServiceServer

	agentManager *agentmanager.Manager
	agentRepo    repositories.AgentRepository
	logger       *zap.Logger
	agentToken   string // shared secret agents must present in gRPC metadata
}

// Config holds the configuration for the gRPC server.
type Config struct {
	// ListenAddr is the address the gRPC server binds to (e.g. ":9090").
	ListenAddr string
	// AgentToken is the shared secret agents must send in the "agent-token"
	// metadata key to authenticate. If empty, authentication is disabled
	// (development mode only — never leave empty in production).
	AgentToken string
}

// New creates a new Server instance with the given dependencies.
func New(
	cfg Config,
	agentManager *agentmanager.Manager,
	agentRepo repositories.AgentRepository,
	logger *zap.Logger,
) *Server {
	return &Server{
		agentManager: agentManager,
		agentRepo:    agentRepo,
		logger:       logger.Named("grpc"),
		agentToken:   cfg.AgentToken,
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

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(s.authUnaryInterceptor),
		grpc.StreamInterceptor(s.authStreamInterceptor),
	)

	proto.RegisterAgentServiceServer(grpcServer, s)

	// Shutdown goroutine: when the context is cancelled (server shutdown),
	// GracefulStop drains in-flight RPCs before closing connections.
	go func() {
		<-ctx.Done()
		s.logger.Info("grpc server shutting down gracefully")
		grpcServer.GracefulStop()
	}()

	s.logger.Info("grpc server listening", zap.String("addr", listenAddr))

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

// validateToken extracts the "agent-token" key from gRPC metadata and
// compares it to the configured shared secret.
//
// Metadata in gRPC is the equivalent of HTTP headers — agents set it
// when creating the ClientConn (see agent/internal/connection/client.go).
func (s *Server) validateToken(ctx context.Context) error {
	// If no token is configured, auth is disabled (development mode).
	if s.agentToken == "" {
		return nil
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing metadata")
	}

	values := md.Get("agent-token")
	if len(values) == 0 || values[0] != s.agentToken {
		return status.Error(codes.Unauthenticated, "invalid agent token")
	}

	return nil
}

// ─── AgentService implementation ─────────────────────────────────────────────

// Register handles the initial agent registration RPC.
// It upserts the agent record in the database and returns the agent's
// persistent ID. On reconnect, the agent passes back its stored ID so the
// server can match it to the existing record instead of creating a duplicate.
//
// Upsert logic: look up by hostname first. If found, update metadata and
// return the existing ID. If not found, create a new record.
func (s *Server) Register(ctx context.Context, req *proto.RegisterRequest) (*proto.RegisterResponse, error) {
	logger := s.logger.With(zap.String("hostname", req.Hostname))

	existing, err := s.agentRepo.GetByHostname(ctx, req.Hostname)
	if err != nil && err != repositories.ErrNotFound {
		logger.Error("failed to look up agent by hostname", zap.Error(err))
		return nil, status.Error(codes.Internal, "registration failed")
	}

	if existing != nil {
		// Agent already known — update its metadata in case the version,
		// OS, or arch changed since the last connection (e.g. after an upgrade).
		existing.Version = req.Version
		existing.OS = req.Os
		existing.Arch = req.Arch

		if err := s.agentRepo.Update(ctx, existing); err != nil {
			logger.Error("failed to update agent record", zap.Error(err))
			return nil, status.Error(codes.Internal, "registration failed")
		}

		logger.Info("agent re-registered", zap.String("agent_id", existing.ID.String()))
		return &proto.RegisterResponse{
			AgentId:   existing.ID.String(),
			AgentName: existing.Name,
		}, nil
	}

	// First-time registration: create a new agent record.
	// The ID is a UUIDv7 generated in the BeforeCreate hook (see db/models.go).
	// Default display name is the hostname — the user can rename it in the GUI.
	agent := &db.Agent{
		Name:     req.Hostname,
		Hostname: req.Hostname,
		Version:  req.Version,
		OS:       req.Os,
		Arch:     req.Arch,
		Status:   "offline", // will transition to "online" when StreamJobs opens
	}

	if err := s.agentRepo.Create(ctx, agent); err != nil {
		logger.Error("failed to create agent record", zap.Error(err))
		return nil, status.Error(codes.Internal, "registration failed")
	}

	logger.Info("agent registered for the first time", zap.String("agent_id", agent.ID.String()))
	return &proto.RegisterResponse{
		AgentId:   agent.ID.String(),
		AgentName: agent.Name,
	}, nil
}

// Heartbeat handles periodic liveness signals from agents.
// It updates the agent's status to "online" and last_seen_at to now.
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
	s.agentManager.Register(req.AgentId, agent.Hostname, stream)

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

	s.logger.Info("StreamJobs stream closed", zap.String("agent_id", req.AgentId))
	return nil
}

// ReportJobStatus handles job lifecycle updates from agents.
// It persists the status change to the database so the GUI can display
// real-time job progress.
//
// TODO(step-5): update job record in database via JobRepository.
// TODO(step-7): publish status change event to WebSocket hub.
func (s *Server) ReportJobStatus(ctx context.Context, req *proto.JobStatusReport) (*proto.JobStatusResponse, error) {
	s.logger.Info("job status report received",
		zap.String("job_id", req.JobId),
		zap.String("agent_id", req.AgentId),
		zap.String("status", req.Status.String()),
		zap.String("message", req.Message),
	)
	return &proto.JobStatusResponse{Ok: true}, nil
}

// StreamLogs handles the client-streaming RPC for job log ingestion.
// The agent streams log entries in real-time during job execution.
// Entries are buffered in memory and flushed to the database in bulk
// when the stream closes to avoid per-row INSERT overhead.
//
// See server/internal/repository/job.go for the BulkCreateLogs implementation.
//
// TODO(step-5): flush buffered entries via JobRepository.BulkCreateLogs.
func (s *Server) StreamLogs(stream proto.AgentService_StreamLogsServer) error {
	var entries []*proto.LogEntry

	for {
		entry, err := stream.Recv()
		if err == io.EOF {
			// Agent closed the stream (job finished) — flush buffered logs.
			break
		}
		if err != nil {
			s.logger.Warn("StreamLogs: recv error",
				zap.Error(err),
				zap.Int("buffered_entries", len(entries)),
			)
			return status.Errorf(codes.Internal, "recv error: %v", err)
		}
		entries = append(entries, entry)
	}

	s.logger.Info("StreamLogs completed",
		zap.Int("entries_received", len(entries)),
	)

	return stream.SendAndClose(&proto.LogStreamResponse{
		EntriesReceived: uint32(len(entries)),
	})
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