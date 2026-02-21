// Package agentmanager maintains the in-memory registry of connected agents.
//
// When an agent connects and opens a StreamJobs stream, the gRPC server
// registers it here. The scheduler uses this registry to dispatch jobs
// to the correct agent by pushing JobAssignment messages onto the open stream.
//
// All state is in-memory and intentionally non-persistent: if the server
// restarts, agents reconnect and re-register automatically via their
// reconnection loop. The persistent agent record (hostname, capabilities, etc.)
// lives in the database and is managed by AgentRepository.
package agentmanager

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// ConnectedAgent represents an agent that has an active gRPC connection
// and an open StreamJobs stream through which jobs can be dispatched.
type ConnectedAgent struct {
	// ID is the persistent UUIDv7 assigned to this agent by the server
	// on first registration and stored in the database.
	ID string

	// Hostname is stored here for logging and display purposes, avoiding
	// a database lookup every time we need to log agent activity.
	Hostname string

	// ConnectedAt is when this agent established the current connection.
	// Reset on every reconnect — not the same as the DB CreatedAt field.
	ConnectedAt time.Time

	// stream is the open server-side StreamJobs stream for this agent.
	// Jobs are dispatched by calling stream.Send(). The stream is closed
	// when the agent disconnects or the context is cancelled.
	//
	// Using the generated gRPC server stream interface allows the manager
	// to remain independent of the concrete gRPC server implementation.
	stream proto.AgentService_StreamJobsServer
}

// Manager is the in-memory registry of currently connected agents.
// It is safe for concurrent use by multiple goroutines (gRPC server +
// scheduler run in separate goroutines).
//
// The zero value is not usable — create instances with New.
type Manager struct {
	mu     sync.RWMutex
	agents map[string]*ConnectedAgent // keyed by agent ID
	logger *zap.Logger
}

// New creates a new Manager instance.
func New(logger *zap.Logger) *Manager {
	return &Manager{
		agents: make(map[string]*ConnectedAgent),
		logger: logger.Named("agentmanager"),
	}
}

// Register adds an agent to the in-memory registry with its open StreamJobs
// stream. If an agent with the same ID is already registered (e.g. duplicate
// connection before the previous one timed out), the old entry is replaced and
// a warning is logged.
//
// Called by the gRPC server when an agent opens a StreamJobs stream.
func (m *Manager) Register(agentID, hostname string, stream proto.AgentService_StreamJobsServer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.agents[agentID]; exists {
		// This can happen if the agent reconnects before the server detects
		// the previous connection as dead (e.g. after a network blip).
		m.logger.Warn("replacing existing agent connection",
			zap.String("agent_id", agentID),
			zap.String("hostname", hostname),
		)
	}

	m.agents[agentID] = &ConnectedAgent{
		ID:          agentID,
		Hostname:    hostname,
		ConnectedAt: time.Now().UTC(),
		stream:      stream,
	}

	m.logger.Info("agent connected",
		zap.String("agent_id", agentID),
		zap.String("hostname", hostname),
		zap.Int("total_connected", len(m.agents)),
	)
}

// Deregister removes an agent from the in-memory registry.
// Called by the gRPC server when the StreamJobs stream closes (agent
// disconnects, network drop, or server-side context cancellation).
func (m *Manager) Deregister(agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, exists := m.agents[agentID]
	if !exists {
		// Already removed — can happen in race between disconnect and timeout.
		return
	}

	delete(m.agents, agentID)

	m.logger.Info("agent disconnected",
		zap.String("agent_id", agentID),
		zap.String("hostname", agent.Hostname),
		zap.Duration("session_duration", time.Since(agent.ConnectedAt)),
		zap.Int("total_connected", len(m.agents)),
	)
}

// Dispatch sends a JobAssignment to a specific agent via its open stream.
// Returns an error if the agent is not connected or if the send fails.
//
// Called by the scheduler when it decides a job should run on this agent.
// If the send fails, the scheduler is responsible for retrying or marking
// the job as failed — this method does not retry internally.
func (m *Manager) Dispatch(agentID string, job *proto.JobAssignment) error {
	m.mu.RLock()
	agent, exists := m.agents[agentID]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("agent %s is not connected", agentID)
	}

	if err := agent.stream.Send(job); err != nil {
		return fmt.Errorf("failed to send job %s to agent %s: %w", job.JobId, agentID, err)
	}

	m.logger.Info("job dispatched to agent",
		zap.String("job_id", job.JobId),
		zap.String("agent_id", agentID),
		zap.String("hostname", agent.Hostname),
	)

	return nil
}

// IsConnected reports whether an agent with the given ID currently has
// an active connection. Used by the scheduler to decide whether to
// dispatch immediately or defer the job.
func (m *Manager) IsConnected(agentID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.agents[agentID]
	return exists
}

// ConnectedAgents returns a snapshot of all currently connected agents.
// The returned slice is a copy — modifications do not affect the registry.
//
// Used by the REST API to populate the agent list with online/offline status.
func (m *Manager) ConnectedAgents() []*ConnectedAgent {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*ConnectedAgent, 0, len(m.agents))
	for _, a := range m.agents {
		// Shallow copy is safe: ConnectedAgent fields are either value types
		// or read-only after registration (stream is never replaced in place).
		cp := *a
		result = append(result, &cp)
	}
	return result
}

// WaitForAgent blocks until the agent with the given ID connects or the
// context is cancelled. Useful in tests and in the scheduler when a job
// is triggered manually for an agent that might be reconnecting.
//
// Polls every 500ms — not a hot loop, acceptable for this use case.
func (m *Manager) WaitForAgent(ctx context.Context, agentID string) error {
	for {
		if m.IsConnected(agentID) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for agent %s to connect: %w", agentID, ctx.Err())
		case <-time.After(500 * time.Millisecond):
			// poll again
		}
	}
}