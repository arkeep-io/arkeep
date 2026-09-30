package integration_test

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// downloadResult is what the server side of a download read.
type downloadResult struct {
	data []byte
	err  error
}

// startServerDownload connects a fake agent, requests a download from it and
// reads the upload with read, which receives the download once the agent has
// opened it. It returns the agent, the correlation ID it was sent, and the
// channel carrying what read returned.
func startServerDownload(t *testing.T, read func(*agentmanager.SnapshotDownload) downloadResult) (*fakeAgent, string, <-chan downloadResult) {
	t.Helper()
	ts := newTestServer(t)
	agent := newFakeAgent(t, ts.addr)
	agentID := agent.register(t)
	jobs, cancel := agent.openStream(t)
	t.Cleanup(cancel)
	pollUntil(t, 3*time.Second, func() bool { return ts.agentMgr.IsConnected(agentID) })

	results := make(chan downloadResult, 1)
	go func() {
		dl, err := ts.agentMgr.RequestSnapshotDownload(context.Background(), agentID, "corr-1", []byte(`{}`))
		if err != nil {
			results <- downloadResult{err: err}
			return
		}
		defer dl.Close()
		results <- read(dl)
	}()

	select {
	case a := <-jobs:
		if a.Type != proto.JobType_JOB_TYPE_DOWNLOAD_SNAPSHOT_FILE {
			t.Fatalf("assignment type = %v, want DOWNLOAD_SNAPSHOT_FILE", a.Type)
		}
		return agent, a.JobId, results
	case <-time.After(3 * time.Second):
		t.Fatal("the agent received no download assignment")
		return nil, "", nil
	}
}

// openUpload opens UploadSnapshotDownload and sends the identifying chunk.
func openUpload(t *testing.T, agent *fakeAgent, corrID string) proto.AgentService_UploadSnapshotDownloadClient {
	t.Helper()
	upload, err := agent.client.UploadSnapshotDownload(context.Background())
	if err != nil {
		t.Fatalf("UploadSnapshotDownload: %v", err)
	}
	if err := upload.Send(&proto.SnapshotDownloadChunk{AgentId: agent.agentID, CorrelationId: corrID}); err != nil {
		t.Fatalf("sending the first chunk: %v", err)
	}
	return upload
}

func readAll(dl *agentmanager.SnapshotDownload) downloadResult {
	data, err := io.ReadAll(dl)
	return downloadResult{data, err}
}

func TestSnapshotDownload_StreamsOverGRPC(t *testing.T) {
	agent, corrID, results := startServerDownload(t, readAll)
	upload := openUpload(t, agent, corrID)

	want := bytes.Repeat([]byte("0123456789"), 100_000)
	for rest := want; len(rest) > 0; {
		n := min(len(rest), 256*1024)
		if err := upload.Send(&proto.SnapshotDownloadChunk{Data: rest[:n]}); err != nil {
			t.Fatalf("Send: %v", err)
		}
		rest = rest[n:]
	}
	if _, err := upload.CloseAndRecv(); err != nil {
		t.Fatalf("CloseAndRecv: %v", err)
	}

	r := <-results
	if r.err != nil || !bytes.Equal(r.data, want) {
		t.Errorf("server read %d bytes, err %v; want %d bytes, nil", len(r.data), r.err, len(want))
	}
}

func TestSnapshotDownload_AgentErrorReachesTheReader(t *testing.T) {
	agent, corrID, results := startServerDownload(t, readAll)
	upload := openUpload(t, agent, corrID)

	if err := upload.Send(&proto.SnapshotDownloadChunk{Data: []byte("partial")}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := upload.Send(&proto.SnapshotDownloadChunk{Error: "restic: dump failed"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, _ = upload.CloseAndRecv()

	r := <-results
	if string(r.data) != "partial" || r.err == nil || r.err.Error() != "restic: dump failed" {
		t.Errorf("server read %q, err %v; want %q and the agent's error", r.data, r.err, "partial")
	}
}

// TestSnapshotDownload_ReaderGoneStopsTheAgent checks the cancellation path:
// once the server side stops reading, the agent's sends fail, which is what
// stops restic dump on a real agent.
func TestSnapshotDownload_ReaderGoneStopsTheAgent(t *testing.T) {
	agent, corrID, results := startServerDownload(t, func(dl *agentmanager.SnapshotDownload) downloadResult {
		buf := make([]byte, 1)
		_, err := dl.Read(buf)
		return downloadResult{buf, err} // then the deferred Close releases the upload
	})
	upload := openUpload(t, agent, corrID)

	chunk := &proto.SnapshotDownloadChunk{Data: bytes.Repeat([]byte("x"), 256*1024)}
	if err := upload.Send(chunk); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	<-results

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := upload.Send(chunk); err != nil {
			return
		}
	}
	t.Fatal("the agent could keep sending after the server stopped reading")
}
