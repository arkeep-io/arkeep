package grpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// Agent identity (SEC-24). With auto-PKI every agent holds its own client
// certificate, and the agent row records the fingerprint of the certificate it
// registered with. Every RPC that names an agent must come from that agent's
// certificate, and every report about a job must come from the agent the job
// was given to, so an enrolled agent can no longer act as another one.
//
// With an external TLS certificate or in insecure mode agents share one secret
// and present no client certificate: there is nothing to bind an identity to,
// so the agent_id in the message is still trusted (see Serve's warning).

var errCertMismatch = status.Error(codes.PermissionDenied, "the client certificate does not belong to this agent")

// peerCertFingerprint returns the hex SHA-256 of the verified client
// certificate of the RPC, or "" when the connection has none.
func peerCertFingerprint(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return ""
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 || len(tlsInfo.State.VerifiedChains[0]) == 0 {
		return ""
	}
	sum := sha256.Sum256(tlsInfo.State.VerifiedChains[0][0].Raw)
	return hex.EncodeToString(sum[:])
}

// authenticateAgent parses the agent_id of an RPC and, with auto-PKI, checks
// that the caller's certificate is the one bound to that agent.
func (s *Server) authenticateAgent(ctx context.Context, rawAgentID string) (uuid.UUID, error) {
	agentID, err := parseAgentID(rawAgentID)
	if err != nil {
		return uuid.UUID{}, status.Error(codes.InvalidArgument, "invalid agent_id")
	}
	if s.autoCerts == nil {
		return agentID, nil
	}

	fingerprint := peerCertFingerprint(ctx)
	if fingerprint == "" {
		return uuid.UUID{}, status.Error(codes.Unauthenticated, "missing client certificate")
	}
	agent, err := s.agentRepo.GetByID(ctx, agentID)
	if errors.Is(err, repositories.ErrNotFound) {
		return uuid.UUID{}, errCertMismatch
	}
	if err != nil {
		s.logger.Error("failed to load agent to authenticate it", zap.String("agent_id", rawAgentID), zap.Error(err))
		return uuid.UUID{}, status.Error(codes.Internal, "agent authentication failed")
	}
	// An unbound agent binds on Register, which every connection starts with.
	if agent.CertFingerprint == nil || *agent.CertFingerprint != fingerprint {
		s.logger.Warn("rejected an RPC from a certificate not bound to the agent", zap.String("agent_id", rawAgentID))
		return uuid.UUID{}, errCertMismatch
	}
	return agentID, nil
}

// authorizeJob checks that the job exists and was given to agentID (SEC-28).
func (s *Server) authorizeJob(ctx context.Context, agentID, jobID uuid.UUID) error {
	job, err := s.jobRepo.GetByID(ctx, jobID)
	if errors.Is(err, repositories.ErrNotFound) {
		return status.Error(codes.NotFound, "job not found")
	}
	if err != nil {
		s.logger.Error("failed to load job to authorize a report", zap.String("job_id", jobID.String()), zap.Error(err))
		return status.Error(codes.Internal, "failed to authorize the report")
	}
	if job.AgentID != agentID {
		s.logger.Warn("rejected a report about a job given to another agent",
			zap.String("job_id", jobID.String()),
			zap.String("agent_id", agentID.String()),
		)
		return status.Error(codes.PermissionDenied, "the job was not given to this agent")
	}
	return nil
}

// authorizeJobDestination is authorizeJob for a report about one destination
// of the job: the destination must also be one the job writes to, so a report
// cannot touch the records of an unrelated destination.
func (s *Server) authorizeJobDestination(ctx context.Context, agentID, jobID, destID uuid.UUID) error {
	if err := s.authorizeJob(ctx, agentID, jobID); err != nil {
		return err
	}
	dests, err := s.jobRepo.ListDestinationsByJob(ctx, jobID)
	if err != nil {
		s.logger.Error("failed to load job destinations to authorize a report", zap.String("job_id", jobID.String()), zap.Error(err))
		return status.Error(codes.Internal, "failed to authorize the report")
	}
	for _, d := range dests {
		if d.DestinationID == destID {
			return nil
		}
	}
	return status.Error(codes.PermissionDenied, "the destination is not part of this job")
}

// bindRegistration applies the certificate binding rules to a Register call:
// existing is the agent named by the request (nil for a new agent). It binds
// an unbound agent to the caller's certificate (trust on first use) and
// refuses a certificate bound to a different agent. It returns the
// fingerprint to bind a newly created agent to ("" without auto-PKI).
func (s *Server) bindRegistration(ctx context.Context, logger *zap.Logger, existing *db.Agent) (string, error) {
	if s.autoCerts == nil {
		return "", nil
	}
	fingerprint := peerCertFingerprint(ctx)
	if fingerprint == "" {
		return "", status.Error(codes.Unauthenticated, "missing client certificate")
	}

	if existing != nil && existing.CertFingerprint != nil {
		if *existing.CertFingerprint != fingerprint {
			logger.Warn("register: rejected a certificate not bound to the agent", zap.String("agent_id", existing.ID.String()))
			return "", errCertMismatch
		}
		return fingerprint, nil
	}

	owner, err := s.agentRepo.GetByCertFingerprint(ctx, fingerprint)
	if err != nil && !errors.Is(err, repositories.ErrNotFound) {
		logger.Error("register: certificate lookup failed", zap.Error(err))
		return "", status.Error(codes.Internal, "registration failed")
	}
	if owner != nil {
		logger.Warn("register: rejected a certificate already bound to another agent", zap.String("bound_agent_id", owner.ID.String()))
		return "", status.Error(codes.PermissionDenied, "the client certificate belongs to another agent")
	}
	if existing == nil {
		return fingerprint, nil
	}

	bound, err := s.agentRepo.BindCertFingerprint(ctx, existing.ID, fingerprint)
	if err != nil {
		logger.Error("register: failed to bind the agent to its certificate", zap.Error(err))
		return "", status.Error(codes.Internal, "registration failed")
	}
	if !bound {
		// Another certificate bound the agent between the read and the bind.
		return "", errCertMismatch
	}
	logger.Info("agent bound to its client certificate", zap.String("agent_id", existing.ID.String()))
	return fingerprint, nil
}
