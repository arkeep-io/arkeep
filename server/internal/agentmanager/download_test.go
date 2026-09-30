package agentmanager

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// captureStream is a StreamJobs stream that hands every sent assignment to
// the test.
type captureStream struct {
	mockStream
	sent chan *proto.JobAssignment
}

func (c *captureStream) Send(a *proto.JobAssignment) error {
	c.sent <- a
	return nil
}

// chunks returns a recv function that yields the given chunks, then io.EOF.
func chunks(cs ...*proto.SnapshotDownloadChunk) func() (*proto.SnapshotDownloadChunk, error) {
	return func() (*proto.SnapshotDownloadChunk, error) {
		if len(cs) == 0 {
			return nil, io.EOF
		}
		c := cs[0]
		cs = cs[1:]
		return c, nil
	}
}

// startDownload registers agent-1 and starts a RequestSnapshotDownload,
// returning the correlation ID the agent was sent and the request's result.
func startDownload(t *testing.T, mgr *Manager, ctx context.Context) (string, <-chan *SnapshotDownload, <-chan error) {
	t.Helper()
	stream := &captureStream{sent: make(chan *proto.JobAssignment, 1)}
	mgr.Register("agent-1", "host1", false, stream)

	dls, errs := make(chan *SnapshotDownload, 1), make(chan error, 1)
	go func() {
		dl, err := mgr.RequestSnapshotDownload(ctx, "agent-1", "corr-1", []byte(`{}`))
		dls <- dl
		errs <- err
	}()

	select {
	case a := <-stream.sent:
		if a.Type != proto.JobType_JOB_TYPE_DOWNLOAD_SNAPSHOT_FILE {
			t.Fatalf("assignment type = %v, want DOWNLOAD_SNAPSHOT_FILE", a.Type)
		}
		return a.JobId, dls, errs
	case <-time.After(time.Second):
		t.Fatal("no download assignment was sent to the agent")
		return "", nil, nil
	}
}

func TestSnapshotDownload_DeliversToWaiter(t *testing.T) {
	mgr := newTestManager()
	corrID, dls, errs := startDownload(t, mgr, context.Background())

	dl := NewSnapshotDownload(
		&proto.SnapshotDownloadChunk{AgentId: "agent-1", CorrelationId: corrID},
		chunks(&proto.SnapshotDownloadChunk{Data: []byte("hello ")}, &proto.SnapshotDownloadChunk{Data: []byte("world")}),
	)
	if !mgr.DeliverSnapshotDownload(dl) {
		t.Fatal("DeliverSnapshotDownload() = false, want true")
	}
	if err := <-errs; err != nil {
		t.Fatalf("RequestSnapshotDownload: %v", err)
	}
	got, err := io.ReadAll(<-dls)
	if err != nil || string(got) != "hello world" {
		t.Errorf("ReadAll() = %q, %v; want %q, nil", got, err, "hello world")
	}

	// A second upload for the same request is rejected.
	if mgr.DeliverSnapshotDownload(dl) {
		t.Error("second DeliverSnapshotDownload() = true, want false")
	}
}

func TestSnapshotDownload_RejectsUnexpectedUploads(t *testing.T) {
	mgr := newTestManager()
	corrID, _, _ := startDownload(t, mgr, context.Background())

	tests := []struct {
		name  string
		first *proto.SnapshotDownloadChunk
	}{
		{"another agent", &proto.SnapshotDownloadChunk{AgentId: "agent-2", CorrelationId: corrID}},
		{"unknown correlation id", &proto.SnapshotDownloadChunk{AgentId: "agent-1", CorrelationId: "nope"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if mgr.DeliverSnapshotDownload(NewSnapshotDownload(tt.first, chunks())) {
				t.Error("DeliverSnapshotDownload() = true, want false")
			}
		})
	}
}

func TestSnapshotDownload_ReadReturnsAgentError(t *testing.T) {
	dl := NewSnapshotDownload(
		&proto.SnapshotDownloadChunk{},
		chunks(&proto.SnapshotDownloadChunk{Data: []byte("part")}, &proto.SnapshotDownloadChunk{Error: "restic: repository is locked"}),
	)
	got, err := io.ReadAll(dl)
	if string(got) != "part" || err == nil || err.Error() != "restic: repository is locked" {
		t.Errorf("ReadAll() = %q, %v; want %q and the agent's error", got, err, "part")
	}
}

func TestSnapshotDownload_GivingUpReleasesTheUpload(t *testing.T) {
	mgr := newTestManager()
	ctx, cancel := context.WithCancel(context.Background())
	corrID, _, errs := startDownload(t, mgr, ctx)

	cancel()
	if err := <-errs; !errors.Is(err, context.Canceled) {
		t.Fatalf("RequestSnapshotDownload error = %v, want context.Canceled", err)
	}

	// An upload arriving after the request gave up finds no waiter.
	dl := NewSnapshotDownload(&proto.SnapshotDownloadChunk{AgentId: "agent-1", CorrelationId: corrID}, chunks())
	if mgr.DeliverSnapshotDownload(dl) {
		t.Error("DeliverSnapshotDownload() after cancel = true, want false")
	}
}
