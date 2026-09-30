package agentmanager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"go.uber.org/zap"

	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// ErrSnapshotDownloadTimeout is returned when the agent does not start
// uploading a DOWNLOAD_SNAPSHOT_FILE request within the deadline.
var ErrSnapshotDownloadTimeout = errors.New("snapshot download request timed out")

// snapshotDownloadTimeout is how long RequestSnapshotDownload waits for the
// agent to start the upload. Opening the repository costs the same as the
// first browse of a snapshot, so the budget is the same.
const snapshotDownloadTimeout = snapshotBrowseTimeout

// pendingDownload is a RequestSnapshotDownload waiting for its upload. The
// agent ID binds the upload to the agent the request was sent to.
type pendingDownload struct {
	agentID string
	ch      chan *SnapshotDownload
}

// SnapshotDownload is an agent's upload of a restic dump, handed by the gRPC
// server to the HTTP download waiting for it. Read returns the dump's bytes,
// then io.EOF once the agent closes the upload, or the error the agent
// reported. Close releases the upload, which ends the gRPC stream.
type SnapshotDownload struct {
	agentID       string
	correlationID string
	recv          func() (*proto.SnapshotDownloadChunk, error)
	pending       []byte
	done          chan struct{}
	closeOnce     sync.Once
}

// NewSnapshotDownload wraps an UploadSnapshotDownload stream whose first chunk
// (carrying the agent and correlation IDs) has already been received; recv
// returns the following chunks.
func NewSnapshotDownload(first *proto.SnapshotDownloadChunk, recv func() (*proto.SnapshotDownloadChunk, error)) *SnapshotDownload {
	return &SnapshotDownload{
		agentID:       first.AgentId,
		correlationID: first.CorrelationId,
		recv:          recv,
		done:          make(chan struct{}),
	}
}

func (d *SnapshotDownload) Read(p []byte) (int, error) {
	for len(d.pending) == 0 {
		chunk, err := d.recv()
		if err != nil {
			return 0, err
		}
		if chunk.Error != "" {
			return 0, errors.New(chunk.Error)
		}
		d.pending = chunk.Data
	}
	n := copy(p, d.pending)
	d.pending = d.pending[n:]
	return n, nil
}

// Close releases the upload. Safe to call more than once.
func (d *SnapshotDownload) Close() {
	d.closeOnce.Do(func() { close(d.done) })
}

// Done is closed by Close.
func (d *SnapshotDownload) Done() <-chan struct{} {
	return d.done
}

var _ io.Reader = (*SnapshotDownload)(nil)

// RequestSnapshotDownload sends a JOB_TYPE_DOWNLOAD_SNAPSHOT_FILE assignment
// to the agent and blocks until the agent starts the upload, which it returns.
// The caller must Close the download once it has relayed it.
//
// Returns ErrAgentNotConnected if the agent is offline, or
// ErrSnapshotDownloadTimeout if the upload does not start within
// snapshotDownloadTimeout.
func (m *Manager) RequestSnapshotDownload(ctx context.Context, agentID, correlationID string, payloadJSON []byte) (*SnapshotDownload, error) {
	m.mu.RLock()
	agent, exists := m.agents[agentID]
	m.mu.RUnlock()

	if !exists {
		return nil, ErrAgentNotConnected
	}

	ch := make(chan *SnapshotDownload, 1)
	m.pendingMu.Lock()
	m.pendingSnapshotDownloads[correlationID] = pendingDownload{agentID: agentID, ch: ch}
	m.pendingMu.Unlock()

	err := agent.send(&proto.JobAssignment{
		JobId:   correlationID,
		Type:    proto.JobType_JOB_TYPE_DOWNLOAD_SNAPSHOT_FILE,
		Payload: payloadJSON,
	})
	if err != nil {
		err = fmt.Errorf("failed to send snapshot download request to agent %s: %w", agentID, err)
	} else {
		timeout := time.NewTimer(snapshotDownloadTimeout)
		defer timeout.Stop()

		select {
		case dl := <-ch:
			return dl, nil
		case <-timeout.C:
			err = ErrSnapshotDownloadTimeout
		case <-ctx.Done():
			err = ctx.Err()
		}
	}

	// Giving up: stop waiting, and release an upload delivered in the meantime,
	// since nothing will ever read it.
	m.pendingMu.Lock()
	delete(m.pendingSnapshotDownloads, correlationID)
	m.pendingMu.Unlock()
	select {
	case dl := <-ch:
		dl.Close()
	default:
	}
	return nil, err
}

// DeliverSnapshotDownload is called by the gRPC server when an agent opens
// UploadSnapshotDownload. It hands the upload to the RequestSnapshotDownload
// waiting for its correlation ID, and reports false when none is waiting for
// it from this agent — the caller must then end the upload.
func (m *Manager) DeliverSnapshotDownload(dl *SnapshotDownload) bool {
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()

	p, ok := m.pendingSnapshotDownloads[dl.correlationID]
	if !ok || p.agentID != dl.agentID {
		m.logger.Warn("DeliverSnapshotDownload: no waiter for correlation_id from this agent, rejecting",
			zap.String("correlation_id", dl.correlationID),
			zap.String("agent_id", dl.agentID),
		)
		return false
	}
	// One upload per request: a second one for the same ID is rejected.
	delete(m.pendingSnapshotDownloads, dl.correlationID)
	p.ch <- dl
	return true
}
