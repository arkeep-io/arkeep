package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// TestJobHandler_CancelReleasesBusyGate reproduces issue #290: a retention
// sweep holding a destination's busy gate is cancelled from the GUI. The gate
// must be released at once, and the destination API must show it free —
// before the fix the cancelled job kept it forever and every later backup to
// the destination was skipped.
func TestJobHandler_CancelReleasesBusyGate(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	agent := createDBAgent(t, e.deps, "retention-agent")
	dest := createDBDestination(t, e.deps, "r2", "local")
	job := &db.Job{AgentID: agent.ID, Type: "retention", Status: "running"}
	if err := e.deps.jobs.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	if acquired, err := e.deps.dests.TryAcquireBusy(ctx, dest.ID, job.ID); err != nil || !acquired {
		t.Fatalf("TryAcquireBusy: acquired=%v err=%v", acquired, err)
	}

	busyJobID := func() string {
		t.Helper()
		resp := e.get(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)
		var d struct {
			BusyJobID string `json:"busy_job_id"`
			BusySince string `json:"busy_since"`
		}
		decodeData(t, resp, &d)
		if (d.BusyJobID == "") != (d.BusySince == "") {
			t.Errorf("busy_job_id = %q but busy_since = %q, want both set or both empty", d.BusyJobID, d.BusySince)
		}
		return d.BusyJobID
	}

	if got := busyJobID(); got != job.ID.String() {
		t.Fatalf("busy_job_id before cancel = %q, want %q", got, job.ID)
	}

	resp := e.post(t, "/api/v1/jobs/"+job.ID.String()+"/cancel", e.adminToken(t), nil)
	assertStatus(t, resp, http.StatusOK)

	if got := busyJobID(); got != "" {
		t.Errorf("busy_job_id after cancel = %q, want the gate released", got)
	}
}

// TestJobHandler_CancelWaitingJob: a job queued behind a busy destination
// (issue #285) can be cancelled, and the status filter lists waiting jobs.
func TestJobHandler_CancelWaitingJob(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	agent := createDBAgent(t, e.deps, "retention-agent")
	job := &db.Job{AgentID: agent.ID, Type: "retention", Status: "pending"}
	if err := e.deps.jobs.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := e.deps.jobs.UpdateStatus(ctx, job.ID, "waiting", nil, nil, ""); err != nil {
		t.Fatalf("queue job: %v", err)
	}

	resp := e.get(t, "/api/v1/jobs?status=waiting", e.adminToken(t))
	assertStatus(t, resp, http.StatusOK)
	var list struct {
		Total int64 `json:"total"`
	}
	decodeData(t, resp, &list)
	if list.Total != 1 {
		t.Errorf("jobs with status=waiting = %d, want 1", list.Total)
	}

	resp = e.post(t, "/api/v1/jobs/"+job.ID.String()+"/cancel", e.adminToken(t), nil)
	assertStatus(t, resp, http.StatusOK)

	stored, err := e.deps.jobs.GetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.Status != "cancelled" {
		t.Errorf("status after cancel = %q, want \"cancelled\"", stored.Status)
	}
}
