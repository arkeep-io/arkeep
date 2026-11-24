package integration_test

import (
	"context"
	"testing"
	"time"

	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// TestReportJobStatus_RunningAfterCancelTellsAgent: the server closed a job
// while it sat in the agent's local queue, and the cancel never reached the
// agent (it was offline, or the cancel raced the assignment). When the agent
// starts the job anyway, its RUNNING report is refused as terminal, and the
// server must tell it to abort: the job no longer holds its destinations, so
// an integrity check or retention sweep may be using the same repository.
// A late outcome (COMPLETED, FAILED) of a closed job is only ignored, as
// before: the job already ended on the agent, there is nothing to abort.
func TestReportJobStatus_RunningAfterCancelTellsAgent(t *testing.T) {
	for _, tc := range []struct {
		name       string
		report     proto.JobStatus
		wantCancel bool
	}{
		{name: "running", report: proto.JobStatus_JOB_STATUS_RUNNING, wantCancel: true},
		{name: "completed", report: proto.JobStatus_JOB_STATUS_COMPLETED, wantCancel: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := newTestServer(t)
			agent := newFakeAgent(t, ts.addr)
			agentID := agent.register(t)
			jobsCh, cancelStream := agent.openStream(t)
			defer cancelStream()
			waitForAgentStatus(t, ts.agentRepo, agentID, "online")

			job := createIntegrationJob(t, ts, mustParseUUID(t, agentID))
			now := time.Now().UTC()
			if err := ts.jobRepo.UpdateStatus(context.Background(), job.ID, "cancelled", nil, &now, ""); err != nil {
				t.Fatalf("cancel job: %v", err)
			}

			agent.reportStatus(t, job.ID.String(), tc.report)

			var cancels []string
			deadline := time.After(500 * time.Millisecond)
		collect:
			for {
				select {
				case a := <-jobsCh:
					if a.Type == proto.JobType_JOB_TYPE_CANCEL {
						cancels = append(cancels, a.JobId)
					}
				case <-deadline:
					break collect
				}
			}

			switch {
			case tc.wantCancel && (len(cancels) != 1 || cancels[0] != job.ID.String()):
				t.Errorf("cancels sent = %v, want one for job %s", cancels, job.ID)
			case !tc.wantCancel && len(cancels) != 0:
				t.Errorf("cancels sent = %v, want none for a late outcome", cancels)
			}
			waitForJobStatus(t, ts.jobRepo, job.ID.String(), "cancelled")
		})
	}
}
