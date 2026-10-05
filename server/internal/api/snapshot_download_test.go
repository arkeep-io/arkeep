package api

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// fakeAgentStream is the StreamJobs stream of an in-process agent that answers
// browse requests with entries and download requests with chunks, without
// gRPC or restic.
type fakeAgentStream struct {
	grpc.ServerStream
	mgr     *agentmanager.Manager
	agentID string
	entries []*proto.SnapshotFileEntry
	chunks  []*proto.SnapshotDownloadChunk

	mu   sync.Mutex
	sent []*proto.JobAssignment
}

// assignments returns every JobAssignment sent to the fake agent so far.
func (f *fakeAgentStream) assignments() []*proto.JobAssignment {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*proto.JobAssignment(nil), f.sent...)
}

func (f *fakeAgentStream) Send(a *proto.JobAssignment) error {
	f.mu.Lock()
	f.sent = append(f.sent, a)
	f.mu.Unlock()
	switch a.Type {
	case proto.JobType_JOB_TYPE_LIST_SNAPSHOT_FILES:
		go f.mgr.DeliverSnapshotBrowse(&proto.SnapshotBrowseReport{AgentId: f.agentID, CorrelationId: a.JobId, Entries: f.entries})
	case proto.JobType_JOB_TYPE_DOWNLOAD_SNAPSHOT_FILE:
		chunks := f.chunks
		recv := func() (*proto.SnapshotDownloadChunk, error) {
			if len(chunks) == 0 {
				return nil, io.EOF
			}
			c := chunks[0]
			chunks = chunks[1:]
			return c, nil
		}
		first := &proto.SnapshotDownloadChunk{AgentId: f.agentID, CorrelationId: a.JobId}
		go f.mgr.DeliverSnapshotDownload(agentmanager.NewSnapshotDownload(first, recv))
	}
	return nil
}

// connectFakeAgent registers an online fake agent and returns its ID.
func connectFakeAgent(e *testEnv, stream *fakeAgentStream) string {
	stream.mgr = e.mgr
	stream.agentID = uuid.NewString()
	e.mgr.Register(stream.agentID, "fake-host", false, stream)
	return stream.agentID
}

// requestDownload asks for a download ticket and returns its URL.
func requestDownload(t *testing.T, e *testEnv, snapshotID, path, typ, agentID string) string {
	t.Helper()
	resp := e.post(t, "/api/v1/snapshots/"+snapshotID+"/download", e.adminToken(t),
		map[string]any{"path": path, "type": typ, "agent_id": agentID})
	assertStatus(t, resp, http.StatusOK)
	var data struct {
		URL string `json:"url"`
	}
	decodeData(t, resp, &data)
	return data.URL
}

func TestSnapshotHandler_CreateDownload(t *testing.T) {
	t.Run("is admin only", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshot(t, e.deps)
		resp := e.post(t, "/api/v1/snapshots/"+s.ID.String()+"/download", e.userToken(t), map[string]any{"path": "/a", "type": "file"})
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("rejects an unknown entry type", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshot(t, e.deps)
		resp := e.post(t, "/api/v1/snapshots/"+s.ID.String()+"/download", e.adminToken(t), map[string]any{"path": "/a", "type": "symlink"})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 422 for an imported snapshot without a stored password", func(t *testing.T) {
		e := newTestEnv(t)
		s, _ := createDBImportedSnapshot(t, e.deps, "")
		agentID := connectFakeAgent(e, &fakeAgentStream{})
		resp := e.post(t, "/api/v1/snapshots/"+s.ID.String()+"/download", e.adminToken(t), map[string]any{"path": "/a", "type": "file", "agent_id": agentID})
		assertStatus(t, resp, http.StatusUnprocessableEntity)
	})

	t.Run("returns 503 when the agent is offline", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshot(t, e.deps)
		resp := e.post(t, "/api/v1/snapshots/"+s.ID.String()+"/download", e.adminToken(t), map[string]any{"path": "/a", "type": "file"})
		assertStatus(t, resp, http.StatusServiceUnavailable)
	})

	t.Run("returns a download URL", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshot(t, e.deps)
		agentID := connectFakeAgent(e, &fakeAgentStream{})
		if url := requestDownload(t, e, s.ID.String(), "/a", "file", agentID); !strings.HasPrefix(url, "/api/v1/downloads/") {
			t.Errorf("url = %q, want a /api/v1/downloads/ ticket URL", url)
		}
	})
}

func TestSnapshotHandler_Download(t *testing.T) {
	t.Run("streams the file with download headers", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshot(t, e.deps)
		agentID := connectFakeAgent(e, &fakeAgentStream{chunks: []*proto.SnapshotDownloadChunk{
			{Data: []byte("hello ")}, {Data: []byte("world")},
		}})
		url := requestDownload(t, e, s.ID.String(), "/etc/résumé.txt", "file", agentID)

		resp := e.get(t, url, "")
		assertStatus(t, resp, http.StatusOK)
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || string(body) != "hello world" {
			t.Errorf("body = %q, %v; want %q", body, err, "hello world")
		}
		if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename*=utf-8''r%C3%A9sum%C3%A9.txt` {
			t.Errorf("Content-Disposition = %q", cd)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", cc)
		}

		// The ticket is single use.
		again := e.get(t, url, "")
		_ = again.Body.Close()
		assertStatus(t, again, http.StatusNotFound)
	})

	t.Run("names a directory archive after the directory", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshot(t, e.deps)
		agentID := connectFakeAgent(e, &fakeAgentStream{chunks: []*proto.SnapshotDownloadChunk{{Data: []byte("PK")}}})
		resp := e.get(t, requestDownload(t, e, s.ID.String(), "/home/alice", "dir", agentID), "")
		_ = resp.Body.Close()
		if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename=alice.zip` {
			t.Errorf("Content-Disposition = %q, want the directory name with .zip", cd)
		}
	})

	t.Run("reports an agent error before any data as 502", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshot(t, e.deps)
		agentID := connectFakeAgent(e, &fakeAgentStream{chunks: []*proto.SnapshotDownloadChunk{{Error: "restic: path not found"}}})
		resp := e.get(t, requestDownload(t, e, s.ID.String(), "/missing", "file", agentID), "")
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusBadGateway)
	})

	t.Run("aborts the response on an agent error mid-stream", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshot(t, e.deps)
		agentID := connectFakeAgent(e, &fakeAgentStream{chunks: []*proto.SnapshotDownloadChunk{
			{Data: []byte("partial")}, {Error: "restic: repository read error"},
		}})
		resp := e.get(t, requestDownload(t, e, s.ID.String(), "/big", "file", agentID), "")
		assertStatus(t, resp, http.StatusOK)
		_, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err == nil {
			t.Error("reading the body succeeded, want the aborted connection to fail it")
		}
	})

	t.Run("returns 404 for an unknown ticket", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/downloads/not-a-ticket", "")
		_ = resp.Body.Close()
		assertStatus(t, resp, http.StatusNotFound)
	})
}

func TestSnapshotHandler_BrowseOnChosenAgent(t *testing.T) {
	e := newTestEnv(t)
	s := createDBSnapshot(t, e.deps) // its policy's agent is offline
	agentID := connectFakeAgent(e, &fakeAgentStream{entries: []*proto.SnapshotFileEntry{{Path: "/etc", Type: "dir"}}})

	resp := e.get(t, "/api/v1/snapshots/"+s.ID.String()+"/browse?agent_id="+agentID, e.userToken(t))
	assertStatus(t, resp, http.StatusOK)
	var data struct {
		Entries []struct {
			Path string `json:"path"`
		} `json:"entries"`
	}
	decodeData(t, resp, &data)
	if len(data.Entries) != 1 || data.Entries[0].Path != "/etc" {
		t.Errorf("entries = %+v, want the chosen agent's listing", data.Entries)
	}
}

func TestDownloadTickets_Expire(t *testing.T) {
	d := newDownloadTickets()
	token, err := d.issue(downloadTicket{path: "/a"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	t0 := d.pending[token]
	t0.expiresAt = t0.expiresAt.Add(-2 * downloadTicketTTL)
	d.pending[token] = t0

	if _, ok := d.redeem(token); ok {
		t.Error("redeem() of an expired ticket = true, want false")
	}
}
