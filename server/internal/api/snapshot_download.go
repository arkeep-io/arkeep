package api

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/auth"
)

// downloadTicketTTL is how long a download ticket can be redeemed. The GUI
// redeems it immediately; the short life limits a leaked URL's usefulness.
const downloadTicketTTL = time.Minute

// downloadTicket is what a ticket authorises: one download of one path.
type downloadTicket struct {
	snapshotID uuid.UUID
	agentID    string
	path       string
	archive    bool
	filename   string
	expiresAt  time.Time
}

// downloadTickets holds the pending download tickets in memory, like the
// agent registry: a ticket only makes sense to the server process that
// issued it.
type downloadTickets struct {
	mu      sync.Mutex
	pending map[string]downloadTicket
}

func newDownloadTickets() *downloadTickets {
	return &downloadTickets{pending: make(map[string]downloadTicket)}
}

// issue stores t and returns its token, dropping expired tickets on the way.
func (d *downloadTickets) issue(t downloadTicket) (string, error) {
	token, err := auth.GenerateResetToken() // the package's generic opaque token
	if err != nil {
		return "", err
	}
	now := time.Now()
	t.expiresAt = now.Add(downloadTicketTTL)

	d.mu.Lock()
	defer d.mu.Unlock()
	for k, p := range d.pending {
		if now.After(p.expiresAt) {
			delete(d.pending, k)
		}
	}
	d.pending[token] = t
	return token, nil
}

// redeem returns the ticket for token and invalidates it: tickets are single use.
func (d *downloadTickets) redeem(token string) (downloadTicket, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	t, ok := d.pending[token]
	delete(d.pending, token)
	if !ok || time.Now().After(t.expiresAt) {
		return downloadTicket{}, false
	}
	return t, true
}

// createDownloadRequest is the body for POST /api/v1/snapshots/{id}/download.
type createDownloadRequest struct {
	Path string `json:"path"`
	// Type is the entry type from the browse listing: "file", or "dir" to
	// download the directory as a ZIP archive.
	Type string `json:"type"`
	// AgentID picks the agent that reads the repository: see openSnapshot.
	AgentID string `json:"agent_id"`
}

type createDownloadResponse struct {
	URL string `json:"url"`
}

// snapshotDownloadPayload is the JSON-encoded payload for
// JOB_TYPE_DOWNLOAD_SNAPSHOT_FILE. Mirrors the struct in the agent's
// connection manager.
type snapshotDownloadPayload struct {
	snapshotRepoAccess
	Path    string `json:"path"`
	Archive bool   `json:"archive"`
}

// CreateDownload handles POST /api/v1/snapshots/{id}/download (admin only).
// It checks that the snapshot can be read by an online agent and returns the
// URL of a single-use ticket that downloads the file (or the directory as a
// ZIP archive). A plain browser download cannot carry the Authorization
// header, and fetching the file into memory would not scale, so the ticket
// is what authorises GET /api/v1/downloads/{ticket}.
func (h *SnapshotHandler) CreateDownload(w http.ResponseWriter, r *http.Request) {
	snapshotID, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	var req createDownloadRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	switch {
	case req.Type != "file" && req.Type != "dir":
		ErrBadRequest(w, `type must be "file" or "dir"`)
		return
	case req.Type == "file" && req.Path == "":
		ErrBadRequest(w, "path is required to download a file")
		return
	case req.Path == "":
		req.Path = "/"
	}

	access, agentID, ok := h.openSnapshot(w, r, snapshotID, req.AgentID, "download")
	if !ok {
		return
	}

	archive := req.Type == "dir"
	token, err := h.tickets.issue(downloadTicket{
		snapshotID: snapshotID,
		agentID:    agentID,
		path:       req.Path,
		archive:    archive,
		filename:   downloadFilename(access.ResticSnapshotID, req.Path, archive),
	})
	if err != nil {
		h.logger.Error("failed to issue download ticket", zap.Error(err))
		ErrInternal(w)
		return
	}

	logAudit(r, h.auditRepo, h.logger, "snapshot.download", "snapshot", snapshotID.String(),
		map[string]any{"path": req.Path, "agent_id": agentID})
	Ok(w, createDownloadResponse{URL: "/api/v1/downloads/" + token})
}

// Download handles GET /api/v1/downloads/{ticket}. The ticket issued by
// CreateDownload is the credential. The agent streams restic dump over
// UploadSnapshotDownload and the bytes are relayed as they arrive, so memory
// stays flat whatever the file size. When the browser goes away, the failed
// write ends the relay, which closes the agent's stream and stops the dump.
func (h *SnapshotHandler) Download(w http.ResponseWriter, r *http.Request) {
	ticket, ok := h.tickets.redeem(chi.URLParam(r, "ticket"))
	if !ok {
		ErrNotFound(w)
		return
	}

	access, agentID, ok := h.openSnapshot(w, r, ticket.snapshotID, ticket.agentID, "download")
	if !ok {
		return
	}
	payloadBytes, err := json.Marshal(snapshotDownloadPayload{
		snapshotRepoAccess: access,
		Path:               ticket.path,
		Archive:            ticket.archive,
	})
	if err != nil {
		h.logger.Error("failed to marshal download payload", zap.Error(err))
		ErrInternal(w)
		return
	}

	// A download lasts as long as the transfer does.
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		h.logger.Warn("download: could not lift the write deadline", zap.Error(err))
	}

	dl, err := h.agentMgr.RequestSnapshotDownload(r.Context(), agentID, uuid.NewString(), payloadBytes)
	if err != nil {
		switch {
		case errors.Is(err, agentmanager.ErrAgentNotConnected):
			ErrServiceUnavailable(w, "agent is not connected")
		case errors.Is(err, agentmanager.ErrSnapshotDownloadTimeout):
			errJSON(w, http.StatusGatewayTimeout, "snapshot download timed out", "gateway_timeout")
		default:
			h.logger.Error("snapshot download failed", zap.Error(err))
			ErrInternal(w)
		}
		return
	}
	defer dl.Close()

	// Read the first bytes before committing to a 200, so a dump that fails
	// at once (a missing path, a locked repository) gets a proper error.
	buf := make([]byte, 256*1024)
	n, err := dl.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		h.logger.Warn("agent reported error during snapshot download", zap.Error(err))
		errJSON(w, http.StatusBadGateway, err.Error(), "agent_error")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": ticket.filename}))
	w.Header().Set("Cache-Control", "no-store")
	// Flushing commits the response, so the browser shows the download as
	// started and a later abort reads as a broken transfer.
	_, copyErr := w.Write(buf[:n])
	if copyErr == nil {
		copyErr = rc.Flush()
	}
	if copyErr == nil && err == nil { // err is io.EOF when the first read was the whole dump
		_, copyErr = io.CopyBuffer(w, dl, buf)
	}
	if copyErr != nil {
		// The headers are gone: aborting the connection is the only way to make
		// the browser fail the download instead of keeping a truncated file.
		h.logger.Warn("snapshot download interrupted", zap.Error(copyErr))
		panic(http.ErrAbortHandler)
	}
}

// downloadFilename names the downloaded file: the entry's own name, with a
// .zip extension for a directory archive; the snapshot root is named after
// the snapshot.
func downloadFilename(resticSnapshotID, p string, archive bool) string {
	name := path.Base(p)
	if name == "/" || name == "." {
		name = "snapshot-" + resticSnapshotID[:min(8, len(resticSnapshotID))]
	}
	if archive {
		name += ".zip"
	}
	return name
}
