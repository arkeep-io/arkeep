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

// ErrSnapshotBrowseTimeout is returned when the agent does not respond to a
// LIST_SNAPSHOT_FILES request within the deadline.
var ErrSnapshotBrowseTimeout = errors.New("snapshot browse request timed out")

// ErrSnapshotImportTimeout is returned when the agent does not respond to a
// IMPORT_SNAPSHOTS request within the deadline.
var ErrSnapshotImportTimeout = errors.New("snapshot import request timed out")

// volumeListTimeout is how long RequestVolumeList waits for the agent to reply.
const volumeListTimeout = 10 * time.Second

// snapshotBrowseTimeout is how long RequestSnapshotBrowse waits for the agent.
// Generous because the first directory expansion loads the repository index,
// which can take minutes on a large remote (e.g. rclone/pCloud) repository.
const snapshotBrowseTimeout = 5 * time.Minute

// snapshotImportTimeout is how long RequestSnapshotImport waits for the agent.
// Matches snapshotBrowseTimeout and the extended HTTP write deadline on the
// import handlers: listing snapshots on a cold remote repository (rclone to a
// cloud provider) plus `restic stats --mode raw-data` routinely takes minutes,
// and a shorter budget makes the request fail while the agent still succeeds —
// its report then arrives with no waiter left and is discarded.
const snapshotImportTimeout = 5 * time.Minute
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

	// session identifies this connection. See SessionToken.
	session SessionToken
}

// SessionToken identifies a single StreamJobs session of an agent.
//
// It exists because a stream's context can expire long after the agent has
// already reconnected: a laptop coming out of sleep reconnects in about a
// second, while the previous connection is only noticed when TCP dead-peer
// detection fires, minutes later. Register hands out a fresh token per
// connection and the teardown path passes it back, so a late cleanup belonging
// to a superseded session cannot deregister the live agent, mark it offline, or
// interrupt the job it has just started.
type SessionToken uint64

// VolumeListResult carries the outcome of a JOB_TYPE_LIST_VOLUMES request.
type VolumeListResult struct {
	Volumes []*proto.VolumeInfo
	Err     string // non-empty when the agent reported an error
}

// SnapshotBrowseResult carries the outcome of a JOB_TYPE_LIST_SNAPSHOT_FILES request.
type SnapshotBrowseResult struct {
	Entries []*proto.SnapshotFileEntry
	Err     string // non-empty when the agent reported an error
}

// SnapshotImportResult carries the outcome of a JOB_TYPE_IMPORT_SNAPSHOTS request.
type SnapshotImportResult struct {
	Snapshots []*proto.ImportedSnapshotInfo
	// RepoSizeBytes is the real deduplicated repo size reported by the agent
	// (from `restic stats`). Zero when unavailable.
	RepoSizeBytes int64
	Err           string // non-empty when the agent reported an error
}

// Manager is the in-memory registry of currently connected agents.
// It is safe for concurrent use by multiple goroutines (gRPC server +
// scheduler run in separate goroutines).
//
// The zero value is not usable — create instances with New.
type Manager struct {
	mu     sync.RWMutex
	agents map[string]*ConnectedAgent // keyed by agent ID
	// nextSession hands out SessionTokens. Guarded by mu.
	nextSession SessionToken
	logger      *zap.Logger

	// pendingMu protects both pending maps.
	// When a REST handler calls RequestVolumeList / RequestSnapshotBrowse, it
	// registers a channel here. The matching Deliver* method sends on the channel
	// when the agent RPC arrives.
	pendingMu               sync.Mutex
	pendingVolumeLists      map[string]chan VolumeListResult     // keyed by correlation ID
	pendingSnapshotBrowses  map[string]chan SnapshotBrowseResult // keyed by correlation ID
	pendingSnapshotImports  map[string]chan SnapshotImportResult // keyed by correlation ID
}

// New creates a new Manager instance.
func New(logger *zap.Logger) *Manager {
	return &Manager{
		agents:                  make(map[string]*ConnectedAgent),
		pendingVolumeLists:      make(map[string]chan VolumeListResult),
		pendingSnapshotBrowses:  make(map[string]chan SnapshotBrowseResult),
		pendingSnapshotImports:  make(map[string]chan SnapshotImportResult),
		logger:                  logger.Named("agentmanager"),
	}
}

// Register adds an agent to the in-memory registry with its open StreamJobs
// stream. If an agent with the same ID is already registered (e.g. duplicate
// connection before the previous one timed out), the old entry is replaced and
// a warning is logged.
//
// Returns the token identifying this session, which must be handed back to
// Deregister when the stream closes.
//
// Called by the gRPC server when an agent opens a StreamJobs stream.
func (m *Manager) Register(agentID, hostname string, dockerAvailable bool, stream proto.AgentService_StreamJobsServer) SessionToken {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.agents[agentID]; exists {
		m.logger.Warn("replacing existing agent connection",
			zap.String("agent_id", agentID),
			zap.String("hostname", hostname),
		)
	}

	m.nextSession++
	session := m.nextSession

	m.agents[agentID] = &ConnectedAgent{
		ID:              agentID,
		Hostname:        hostname,
		ConnectedAt:     time.Now().UTC(),
		DockerAvailable: dockerAvailable,
		stream:          stream,
		session:         session,
	}

	m.logger.Info("agent connected",
		zap.String("agent_id", agentID),
		zap.String("hostname", hostname),
		zap.Bool("docker", dockerAvailable),
		zap.Uint64("session", uint64(session)),
		zap.Int("total_connected", len(m.agents)),
	)

	return session
}

// Deregister removes an agent from the in-memory registry, but only if session
// is the one currently registered. It reports whether the removal happened.
//
// A false return means the stream that is tearing down was already superseded by
// a newer connection from the same agent — the caller must then leave the agent
// and its jobs alone. See SessionToken.
//
// Called by the gRPC server when the StreamJobs stream closes.
func (m *Manager) Deregister(agentID string, session SessionToken) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, exists := m.agents[agentID]
	if !exists {
		return false
	}
	if agent.session != session {
		m.logger.Info("ignoring teardown of a superseded agent session",
			zap.String("agent_id", agentID),
			zap.Uint64("closing_session", uint64(session)),
			zap.Uint64("current_session", uint64(agent.session)),
		)
		return false
	}

	delete(m.agents, agentID)

	m.logger.Info("agent disconnected",
		zap.String("agent_id", agentID),
		zap.String("hostname", agent.Hostname),
		zap.Uint64("session", uint64(session)),
		zap.Duration("session_duration", time.Since(agent.ConnectedAt)),
		zap.Int("total_connected", len(m.agents)),
	)

	return true
}

// DeregisterStale removes an agent from the in-memory registry, but only if
// its current connection was already established when the caller started
// deciding to remove it (before). It reports whether the removal happened.
//
// Used by the heartbeat watchdog, which — unlike the gRPC server's own
// StreamJobs teardown — has no session token to check: it decides an agent is
// stale purely from how long ago its last heartbeat arrived, then acts on
// that decision slightly later. Pass the time the watchdog captured at the
// start of that decision (not the heartbeat staleness cutoff, which can be
// minutes in the past and would reject every real stale connection): if the
// agent reconnects in between, its ConnectedAt moves to after that mark, and
// this call must leave the fresh connection and its jobs alone rather than
// ripping out a connection the watchdog never actually observed as stale.
func (m *Manager) DeregisterStale(agentID string, before time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, exists := m.agents[agentID]
	if !exists {
		return false
	}
	if agent.ConnectedAt.After(before) {
		m.logger.Info("ignoring stale-agent deregister: a newer connection exists",
			zap.String("agent_id", agentID),
			zap.Time("connected_at", agent.ConnectedAt),
			zap.Time("before", before),
		)
		return false
	}

	delete(m.agents, agentID)

	m.logger.Info("agent disconnected (heartbeat timeout)",
		zap.String("agent_id", agentID),
		zap.String("hostname", agent.Hostname),
		zap.Duration("session_duration", time.Since(agent.ConnectedAt)),
		zap.Int("total_connected", len(m.agents)),
	)

	return true
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

// SendCancel sends a JOB_TYPE_CANCEL message to the agent, instructing it to
// abort the job identified by jobID. Returns ErrAgentNotConnected if the agent
// is offline — in that case the caller should only update the DB status.
func (m *Manager) SendCancel(agentID, jobID string) error {
	m.mu.RLock()
	agent, exists := m.agents[agentID]
	m.mu.RUnlock()

	if !exists {
		return ErrAgentNotConnected
	}

	if err := agent.stream.Send(&proto.JobAssignment{
		JobId: jobID,
		Type:  proto.JobType_JOB_TYPE_CANCEL,
	}); err != nil {
		return fmt.Errorf("failed to send cancel signal for job %s to agent %s: %w", jobID, agentID, err)
	}

	m.logger.Info("cancel signal sent to agent",
		zap.String("job_id", jobID),
		zap.String("agent_id", agentID),
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

// RequestSnapshotBrowse sends a JOB_TYPE_LIST_SNAPSHOT_FILES assignment to the
// agent and blocks until the agent responds via ReportSnapshotBrowse or the
// request times out.
//
// payloadJSON is the JSON-encoded browse payload (restic_snapshot_id, repo_password,
// destination). correlationID must be a unique UUID per call.
//
// Returns ErrAgentNotConnected if the agent is offline, or
// ErrSnapshotBrowseTimeout if the agent does not respond within
// snapshotBrowseTimeout.
func (m *Manager) RequestSnapshotBrowse(ctx context.Context, agentID, correlationID string, payloadJSON []byte) (SnapshotBrowseResult, error) {
	m.mu.RLock()
	agent, exists := m.agents[agentID]
	m.mu.RUnlock()

	if !exists {
		return SnapshotBrowseResult{}, ErrAgentNotConnected
	}

	ch := make(chan SnapshotBrowseResult, 1)
	m.pendingMu.Lock()
	m.pendingSnapshotBrowses[correlationID] = ch
	m.pendingMu.Unlock()

	defer func() {
		m.pendingMu.Lock()
		delete(m.pendingSnapshotBrowses, correlationID)
		m.pendingMu.Unlock()
	}()

	assignment := &proto.JobAssignment{
		JobId:   correlationID,
		Type:    proto.JobType_JOB_TYPE_LIST_SNAPSHOT_FILES,
		Payload: payloadJSON,
	}
	if err := agent.stream.Send(assignment); err != nil {
		return SnapshotBrowseResult{}, fmt.Errorf("failed to send snapshot browse request to agent %s: %w", agentID, err)
	}

	m.logger.Debug("snapshot browse request sent",
		zap.String("agent_id", agentID),
		zap.String("correlation_id", correlationID),
	)

	timeout := time.NewTimer(snapshotBrowseTimeout)
	defer timeout.Stop()

	select {
	case result := <-ch:
		return result, nil
	case <-timeout.C:
		return SnapshotBrowseResult{}, ErrSnapshotBrowseTimeout
	case <-ctx.Done():
		return SnapshotBrowseResult{}, ctx.Err()
	}
}

// DeliverSnapshotBrowse is called by the gRPC server when it receives a
// ReportSnapshotBrowse RPC from an agent. It matches the report to the waiting
// RequestSnapshotBrowse call via the correlation_id and delivers the result.
func (m *Manager) DeliverSnapshotBrowse(report *proto.SnapshotBrowseReport) {
	m.pendingMu.Lock()
	ch, ok := m.pendingSnapshotBrowses[report.CorrelationId]
	m.pendingMu.Unlock()

	if !ok {
		m.logger.Warn("DeliverSnapshotBrowse: no waiter for correlation_id, discarding",
			zap.String("correlation_id", report.CorrelationId),
			zap.String("agent_id", report.AgentId),
		)
		return
	}

	ch <- SnapshotBrowseResult{
		Entries: report.Entries,
		Err:     report.Error,
	}
}

// RequestSnapshotImport sends a JOB_TYPE_IMPORT_SNAPSHOTS assignment to the
// agent and blocks until the agent responds via ReportSnapshotImport or the
// request times out.
//
// payloadJSON is the JSON-encoded import payload (type, repo_url, env including
// RESTIC_PASSWORD). correlationID must be a unique UUID per call.
//
// Returns ErrAgentNotConnected if the agent is offline, or
// ErrSnapshotImportTimeout if the agent does not respond within
// snapshotImportTimeout.
func (m *Manager) RequestSnapshotImport(ctx context.Context, agentID, correlationID string, payloadJSON []byte) (SnapshotImportResult, error) {
	m.mu.RLock()
	agent, exists := m.agents[agentID]
	m.mu.RUnlock()

	if !exists {
		return SnapshotImportResult{}, ErrAgentNotConnected
	}

	ch := make(chan SnapshotImportResult, 1)
	m.pendingMu.Lock()
	m.pendingSnapshotImports[correlationID] = ch
	m.pendingMu.Unlock()

	defer func() {
		m.pendingMu.Lock()
		delete(m.pendingSnapshotImports, correlationID)
		m.pendingMu.Unlock()
	}()

	assignment := &proto.JobAssignment{
		JobId:   correlationID,
		Type:    proto.JobType_JOB_TYPE_IMPORT_SNAPSHOTS,
		Payload: payloadJSON,
	}
	if err := agent.stream.Send(assignment); err != nil {
		return SnapshotImportResult{}, fmt.Errorf("failed to send snapshot import request to agent %s: %w", agentID, err)
	}

	m.logger.Debug("snapshot import request sent",
		zap.String("agent_id", agentID),
		zap.String("correlation_id", correlationID),
	)

	timeout := time.NewTimer(snapshotImportTimeout)
	defer timeout.Stop()

	select {
	case result := <-ch:
		return result, nil
	case <-timeout.C:
		return SnapshotImportResult{}, ErrSnapshotImportTimeout
	case <-ctx.Done():
		return SnapshotImportResult{}, ctx.Err()
	}
}

// DeliverSnapshotImport is called by the gRPC server when it receives a
// ReportSnapshotImport RPC from an agent. It matches the report to the waiting
// RequestSnapshotImport call via the correlation_id and delivers the result.
func (m *Manager) DeliverSnapshotImport(report *proto.SnapshotImportReport) {
	m.pendingMu.Lock()
	ch, ok := m.pendingSnapshotImports[report.CorrelationId]
	m.pendingMu.Unlock()

	if !ok {
		m.logger.Warn("DeliverSnapshotImport: no waiter for correlation_id, discarding",
			zap.String("correlation_id", report.CorrelationId),
			zap.String("agent_id", report.AgentId),
		)
		return
	}

	ch <- SnapshotImportResult{
		Snapshots:     report.Snapshots,
		RepoSizeBytes: report.RepoSizeBytes,
		Err:           report.Error,
	}
}