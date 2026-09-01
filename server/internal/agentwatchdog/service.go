// Package agentwatchdog provides a background service that detects agents
// which have stopped sending heartbeats and marks them offline.
//
// Without this service, an agent is only marked offline when its gRPC
// StreamJobs stream closes — which relies on the underlying TCP connection
// actually tearing down (a clean shutdown, or a same-network process kill that
// makes the OS send a FIN). A network partition, a crashed host, or an
// unplugged cable leaves no FIN to observe, so without an independent
// timeout the agent would stay "online" forever. This service is that
// timeout: it flips an agent offline once its last_seen_at is older than
// staleTimeout, matching the heartbeat contract documented in
// agent/internal/connection/manager.go (heartbeatInterval).
package agentwatchdog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
)

const (
	// heartbeatInterval mirrors agent/internal/connection/manager.go's constant
	// of the same name — the agent sends a Heartbeat RPC at this cadence.
	heartbeatInterval = 30 * time.Second

	// staleTimeout is how long an online agent can go without a heartbeat
	// before it is declared offline. 3x heartbeatInterval matches the contract
	// already documented (but never implemented until this package) in
	// agent/internal/connection/manager.go: "The server marks an agent offline
	// if no heartbeat arrives within 3x this interval."
	staleTimeout = 3 * heartbeatInterval

	// runInterval is how often the watchdog sweeps for stale agents. Ticking at
	// the same cadence as the heartbeat keeps worst-case detection latency
	// bounded to roughly staleTimeout + runInterval.
	runInterval = heartbeatInterval
)

// agentStore is the subset of the agent repository the watchdog needs.
type agentStore interface {
	ListStale(ctx context.Context, cutoff time.Time) ([]db.Agent, error)
	MarkOfflineIfStale(ctx context.Context, id uuid.UUID, cutoff time.Time) (bool, error)
}

// jobStore is the subset of the job repository the watchdog needs for orphan
// recovery — jobs left "running" by an agent that will never report a
// terminal status again.
type jobStore interface {
	FailRunningJobsForAgent(ctx context.Context, agentID uuid.UUID, errMsg string) (int64, error)
}

// deregisterer removes an agent from the in-memory connection registry so the
// scheduler stops dispatching new jobs to it. Implemented by *agentmanager.Manager.
type deregisterer interface {
	Deregister(agentID string)
}

// notifier sends the agent-offline notification. Implemented by
// notification.Service.
type notifier interface {
	NotifyAgentOffline(ctx context.Context, agentID uuid.UUID, agentName string) error
}

// publisher pushes a live status update to subscribed GUI clients.
// Implemented by *websocket.Hub.
type publisher interface {
	Publish(topic string, msg websocket.Message)
}

// Service periodically detects agents that stopped heartbeating and marks
// them offline, running the same cleanup as a clean StreamJobs disconnect:
// deregister, fail orphaned running jobs, send the offline notification, and
// push a live status update to the GUI.
type Service struct {
	agents   agentStore
	jobs     jobStore
	registry deregisterer
	notif    notifier // may be nil — notifications are then skipped
	pub      publisher
	logger   *zap.Logger
}

// NewService creates a stale-agent watchdog Service. notif may be nil, in
// which case offline notifications are silently skipped (matching how the
// gRPC server treats a nil notification.Service elsewhere in this codebase).
func NewService(agents agentStore, jobs jobStore, registry deregisterer, notif notifier, pub publisher, logger *zap.Logger) *Service {
	return &Service{
		agents:   agents,
		jobs:     jobs,
		registry: registry,
		notif:    notif,
		pub:      pub,
		logger:   logger.Named("agentwatchdog"),
	}
}

// Start runs an initial sweep, then repeats every runInterval until ctx is
// cancelled. Launch it as a goroutine:
//
//	go svc.Start(ctx)
func (s *Service) Start(ctx context.Context) {
	ticker := time.NewTicker(runInterval)
	defer ticker.Stop()

	// Initial sweep shortly after startup catches agents that were already
	// stale before this server instance came up (e.g. after a restart).
	s.RunOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.RunOnce(ctx)
		}
	}
}

// RunOnce performs a single sweep for stale agents and returns how many were
// flipped offline. Safe to call directly (e.g. from tests).
func (s *Service) RunOnce(ctx context.Context) int {
	cutoff := time.Now().UTC().Add(-staleTimeout)

	candidates, err := s.agents.ListStale(ctx, cutoff)
	if err != nil {
		s.logger.Error("failed to list stale agents", zap.Error(err))
		return 0
	}

	flipped := 0
	for _, agent := range candidates {
		ok, err := s.agents.MarkOfflineIfStale(ctx, agent.ID, cutoff)
		if err != nil {
			s.logger.Error("failed to mark agent offline",
				zap.String("agent_id", agent.ID.String()),
				zap.Error(err),
			)
			continue
		}
		if !ok {
			// The agent sent a heartbeat between ListStale and this write —
			// it is no longer stale. Not an error, just a closed race window.
			continue
		}

		s.registry.Deregister(agent.ID.String())

		if n, err := s.jobs.FailRunningJobsForAgent(ctx, agent.ID, "agent disconnected"); err != nil {
			s.logger.Warn("failed to recover orphaned jobs",
				zap.String("agent_id", agent.ID.String()),
				zap.Error(err),
			)
		} else if n > 0 {
			s.logger.Info("recovered orphaned jobs",
				zap.String("agent_id", agent.ID.String()),
				zap.Int64("count", n),
			)
		}

		if s.notif != nil {
			if err := s.notif.NotifyAgentOffline(ctx, agent.ID, agent.Hostname); err != nil {
				s.logger.Warn("failed to send agent-offline notification",
					zap.String("agent_id", agent.ID.String()),
					zap.Error(err),
				)
			}
		}

		s.pub.Publish("agent:"+agent.ID.String(), websocket.Message{
			Type:    websocket.MsgAgentStatus,
			Payload: map[string]any{"status": "offline"},
		})

		flipped++
	}

	if flipped > 0 {
		s.logger.Info("marked stale agents offline", zap.Int("count", flipped))
	}
	return flipped
}
