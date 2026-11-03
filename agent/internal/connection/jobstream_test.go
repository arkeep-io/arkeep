package connection

import (
	"context"
	"fmt"
	"io"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/arkeep-io/arkeep/agent/internal/executor"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// fakeJobStream delivers its assignments, then ends.
type fakeJobStream struct {
	grpc.ServerStreamingClient[proto.JobAssignment]
	assignments []*proto.JobAssignment
}

func (s *fakeJobStream) Recv() (*proto.JobAssignment, error) {
	if len(s.assignments) == 0 {
		return nil, io.EOF
	}
	a := s.assignments[0]
	s.assignments = s.assignments[1:]
	return a, nil
}

// fakeAgentClient serves StreamJobs from a fakeJobStream and records the job
// statuses the agent reports.
type fakeAgentClient struct {
	proto.AgentServiceClient
	stream   *fakeJobStream
	statuses []*proto.JobStatusReport
}

func (c *fakeAgentClient) StreamJobs(context.Context, *proto.StreamJobsRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[proto.JobAssignment], error) {
	return c.stream, nil
}

func (c *fakeAgentClient) ReportJobStatus(_ context.Context, r *proto.JobStatusReport, _ ...grpc.CallOption) (*proto.JobStatusResponse, error) {
	c.statuses = append(c.statuses, r)
	return &proto.JobStatusResponse{}, nil
}

// TestJobStreamLoop_FullQueueFailsTheJob covers a job sent while the agent's
// queue is full: it is reported failed at once, so the server releases its
// destinations instead of leaving it pending until the queue timeout.
func TestJobStreamLoop_FullQueueFailsTheJob(t *testing.T) {
	exec := executor.New(nil, nil, nil, zap.NewNop(), "", "")
	for i := 0; ; i++ {
		if err := exec.Enqueue(executor.JobAssignment{JobID: fmt.Sprintf("queued-%d", i)}); err != nil {
			break
		}
	}
	client := &fakeAgentClient{stream: &fakeJobStream{assignments: []*proto.JobAssignment{
		{JobId: "one-too-many", Type: proto.JobType_JOB_TYPE_BACKUP},
	}}}
	m := &Manager{exec: exec, logger: zap.NewNop(), client: client, agentID: "agent-1", sessionCtx: context.Background()}

	_ = m.jobStreamLoop(context.Background(), client, "agent-1")

	if len(client.statuses) != 1 {
		t.Fatalf("reported %d job statuses, want one", len(client.statuses))
	}
	got := client.statuses[0]
	if got.JobId != "one-too-many" || got.Status != proto.JobStatus_JOB_STATUS_FAILED {
		t.Errorf("reported %s as %v, want one-too-many failed", got.JobId, got.Status)
	}
}
