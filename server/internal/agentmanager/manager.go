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
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// ErrAgentNotConnected is returned when an operation targets an agent that has
// no active gRPC connection.
var ErrAgentNotConnected = errors.New("agent not connected")

// ErrVolumeListTimeout is returned when the agent does not respond to a
// LIST_VOLUMES request within the deadline.
var ErrVolumeListTimeout = errors.New("volume list request timed out")

// volumeListTimeout is how long RequestVolumeList waits for the agent to reply.
const volumeListTimeout = 10 * time.Second

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

	// DockerAvailable mirrors the AgentCapabilities.docker field advertised
	// during Register. Stored here so the REST handler can check it without
	// a database lookup.
	DockerAvailable bool

	// stream is the open server-side StreamJobs stream for this agent.
	// Jobs are dispatched by calling stream.Send(). The stream is closed
	// when the agent disconnects or the context is cancelled.
	stream proto.AgentService_StreamJobsServer
}

// VolumeListResult carries the outcome of a JOB_TYPE_LIST_VOLUMES request.
type VolumeListResult struct {
	Volumes []*proto.VolumeInfo
	Err     string // non-empty when the agent reported an error
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

	// pendingVolumeLists maps correlation IDs to response channels.
	// When the REST handler calls RequestVolumeList, it registers a channel here.
	// When the agent calls ReportVolumeList via gRPC, DeliverVolumeList sends
	// the result on the matching channel and removes the entry.
	pendingMu          sync.Mutex
	pendingVolumeLists map[string]chan VolumeListResult // keyed by correlation ID
}

// New creates a new Manager instance.
func New(logger *zap.Logger) *Manager {
	return &Manager{
		agents:             make(map[string]*ConnectedAgent),
		pendingVolumeLists: make(map[string]chan VolumeListResult),
		logger:             logger.Named("agentmanager"),
	}
}

// Register adds an agent to the in-memory registry with its open StreamJobs
// stream. If an agent with the same ID is already registered (e.g. duplicate
// connection before the previous one timed out), the old entry is replaced and
// a warning is logged.
//
// Called by the gRPC server when an agent opens a StreamJobs stream.
func (m *Manager) Register(agentID, hostname string, dockerAvailable bool, stream proto.AgentService_StreamJobsServer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.agents[agentID]; exists {
		m.logger.Warn("replacing existing agent connection",
			zap.String("agent_id", agentID),
			zap.String("hostname", hostname),
		)
	}

	m.agents[agentID] = &ConnectedAgent{
		ID:              agentID,
		Hostname:        hostname,
		ConnectedAt:     time.Now().UTC(),
		DockerAvailable: dockerAvailable,
		stream:          stream,
	}

	m.logger.Info("agent connected",
		zap.String("agent_id", agentID),
		zap.String("hostname", hostname),
		zap.Bool("docker", dockerAvailable),
		zap.Int("total_connected", len(m.agents)),
	)
}

// Deregister removes an agent from the in-memory registry.
// Called by the gRPC server when the StreamJobs stream closes.
func (m *Manager) Deregister(agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, exists := m.agents[agentID]
	if !exists {
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
// an active connection.
func (m *Manager) IsConnected(agentID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.agents[agentID]
	return exists
}

// DockerAvailable reports whether the connected agent advertised Docker support.
// Returns false if the agent is not connected.
func (m *Manager) DockerAvailable(agentID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, exists := m.agents[agentID]
	return exists && a.DockerAvailable
}

// ConnectedAgentsCount returns the number of currently connected agents.
func (m *Manager) ConnectedAgentsCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.agents)
}

// ConnectedAgents returns a snapshot of all currently connected agents.
// The returned slice is a copy — modifications do not affect the registry.
func (m *Manager) ConnectedAgents() []*ConnectedAgent {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*ConnectedAgent, 0, len(m.agents))
	for _, a := range m.agents {
		cp := *a
		result = append(result, &cp)
	}
	return result
}

// WaitForAgent blocks until the agent with the given ID connects or the
// context is cancelled.
func (m *Manager) WaitForAgent(ctx context.Context, agentID string) error {
	for {
		if m.IsConnected(agentID) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for agent %s to connect: %w", agentID, ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// RequestVolumeList sends a JOB_TYPE_LIST_VOLUMES assignment to the agent and
// blocks until the agent responds via ReportVolumeList or the request times out.
//
// correlationID must be unique per call — a UUID is recommended. The agent
// echoes it back in VolumeListReport.correlation_id so the response can be
// matched to this waiting goroutine.
//
// Returns ErrAgentNotConnected if the agent is offline, or ErrVolumeListTimeout
// if the agent does not respond within volumeListTimeout.
func (m *Manager) RequestVolumeList(ctx context.Context, agentID, correlationID string) (VolumeListResult, error) {
	m.mu.RLock()
	agent, exists := m.agents[agentID]
	m.mu.RUnlock()

	if !exists {
		return VolumeListResult{}, ErrAgentNotConnected
	}

	// Register the response channel before sending the request to avoid a race
	// where the agent responds before we start listening.
	ch := make(chan VolumeListResult, 1)
	m.pendingMu.Lock()
	m.pendingVolumeLists[correlationID] = ch
	m.pendingMu.Unlock()

	defer func() {
		m.pendingMu.Lock()
		delete(m.pendingVolumeLists, correlationID)
		m.pendingMu.Unlock()
	}()

	// Send the request via the existing StreamJobs stream. The job_id field
	// carries the correlation_id — the agent echoes it back in VolumeListReport.
	assignment := &proto.JobAssignment{
		JobId: correlationID,
		Type:  proto.JobType_JOB_TYPE_LIST_VOLUMES,
	}
	if err := agent.stream.Send(assignment); err != nil {
		return VolumeListResult{}, fmt.Errorf("failed to send volume list request to agent %s: %w", agentID, err)
	}

	m.logger.Debug("volume list request sent",
		zap.String("agent_id", agentID),
		zap.String("correlation_id", correlationID),
	)

	// Wait for the agent to respond or the deadline to expire.
	timeout := time.NewTimer(volumeListTimeout)
	defer timeout.Stop()

	select {
	case result := <-ch:
		return result, nil
	case <-timeout.C:
		return VolumeListResult{}, ErrVolumeListTimeout
	case <-ctx.Done():
		return VolumeListResult{}, ctx.Err()
	}
}

// DeliverVolumeList is called by the gRPC server when it receives a
// ReportVolumeList RPC from an agent. It matches the report to the waiting
// RequestVolumeList call via the correlation_id and delivers the result.
//
// If no waiter is found (e.g. the REST request already timed out), the
// report is silently discarded.
func (m *Manager) DeliverVolumeList(report *proto.VolumeListReport) {
	m.pendingMu.Lock()
	ch, ok := m.pendingVolumeLists[report.CorrelationId]
	m.pendingMu.Unlock()

	if !ok {
		m.logger.Warn("DeliverVolumeList: no waiter for correlation_id, discarding",
			zap.String("correlation_id", report.CorrelationId),
			zap.String("agent_id", report.AgentId),
		)
		return
	}

	ch <- VolumeListResult{
		Volumes: report.Volumes,
		Err:     report.Error,
	}
}