package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// busyFixture creates an agent, a destination and a running job that holds the
// destination's busy gate. name keeps the records distinct within one test.
func busyFixture(t *testing.T, destRepo DestinationRepository, jobRepo JobRepository, agentRepo AgentRepository, name string) (*db.Destination, *db.Job) {
	t.Helper()
	ctx := context.Background()
	agent := newTestAgentForRetention(t, agentRepo, "agent-"+name)
	dest := &db.Destination{Name: "d-" + name, Type: "local", Config: "{}", Enabled: true}
	if err := destRepo.Create(ctx, dest); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	holder := &db.Job{AgentID: agent.ID, Type: "retention", Status: "running"}
	if err := jobRepo.Create(ctx, holder); err != nil {
		t.Fatalf("create holder job: %v", err)
	}
	if acquired, err := destRepo.TryAcquireBusy(ctx, dest.ID, holder.ID); err != nil || !acquired {
		t.Fatalf("TryAcquireBusy holder: acquired=%v err=%v", acquired, err)
	}
	return dest, holder
}

// setJobStatus writes a job's status directly, bypassing UpdateStatus (which
// would itself release the gate) to reproduce a gate left behind by a job that
// ended through some other path.
func setJobStatus(t *testing.T, jobRepo JobRepository, job *db.Job, status string) {
	t.Helper()
	r := jobRepo.(*gormJobRepository)
	if err := r.db.Model(&db.Job{}).Where("id = ?", job.ID).Update("status", status).Error; err != nil {
		t.Fatalf("set job status %q: %v", status, err)
	}
}

func busyHolder(t *testing.T, destRepo DestinationRepository, dest *db.Destination) string {
	t.Helper()
	got, err := destRepo.GetByID(context.Background(), dest.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.BusyJobID == nil {
		return ""
	}
	return got.BusyJobID.String()
}

func TestTryAcquireBusy_TakesOverStaleGate(t *testing.T) {
	cases := []struct {
		holderStatus string // "" means the holder job row was deleted
		wantAcquired bool
	}{
		{"pending", false},
		{"running", false},
		{"succeeded", true},
		{"failed", true},
		{"cancelled", true},
		{"interrupted", true},
		{"", true},
	}
	for _, tc := range cases {
		name := tc.holderStatus
		if name == "" {
			name = "deleted"
		}
		t.Run(name, func(t *testing.T) {
			gdb := newTestDB(t)
			destRepo := NewDestinationRepository(gdb)
			jobRepo := NewJobRepository(gdb)
			agentRepo := NewAgentRepository(gdb)
			ctx := context.Background()

			dest, holder := busyFixture(t, destRepo, jobRepo, agentRepo, name)
			if tc.holderStatus == "" {
				if err := gdb.Delete(&db.Job{}, "id = ?", holder.ID).Error; err != nil {
					t.Fatalf("delete holder job: %v", err)
				}
			} else {
				setJobStatus(t, jobRepo, holder, tc.holderStatus)
			}

			next := &db.Job{AgentID: holder.AgentID, Type: "backup", Status: "pending"}
			if err := jobRepo.Create(ctx, next); err != nil {
				t.Fatalf("create next job: %v", err)
			}
			acquired, err := destRepo.TryAcquireBusy(ctx, dest.ID, next.ID)
			if err != nil {
				t.Fatalf("TryAcquireBusy: %v", err)
			}
			if acquired != tc.wantAcquired {
				t.Errorf("TryAcquireBusy with holder %s = %v, want %v", name, acquired, tc.wantAcquired)
			}
			wantHolder := holder.ID.String()
			if tc.wantAcquired {
				wantHolder = next.ID.String()
			}
			if got := busyHolder(t, destRepo, dest); got != wantHolder {
				t.Errorf("busy_job_id = %q, want %q", got, wantHolder)
			}
		})
	}
}

func TestUpdateStatus_TerminalReleasesBusyGate(t *testing.T) {
	gdb := newTestDB(t)
	destRepo := NewDestinationRepository(gdb)
	jobRepo := NewJobRepository(gdb)
	agentRepo := NewAgentRepository(gdb)
	ctx := context.Background()

	dest, holder := busyFixture(t, destRepo, jobRepo, agentRepo, "a")
	other, otherHolder := busyFixture(t, destRepo, jobRepo, agentRepo, "b")

	// A non-terminal transition keeps the gate.
	now := time.Now()
	if err := jobRepo.UpdateStatus(ctx, holder.ID, "running", &now, nil, ""); err != nil {
		t.Fatalf("UpdateStatus running: %v", err)
	}
	if got := busyHolder(t, destRepo, dest); got != holder.ID.String() {
		t.Fatalf("busy_job_id after running = %q, want still held", got)
	}

	// Cancelling (the path of the issue #290 reproduction) releases it.
	if err := jobRepo.UpdateStatus(ctx, holder.ID, "cancelled", nil, &now, ""); err != nil {
		t.Fatalf("UpdateStatus cancelled: %v", err)
	}
	if got := busyHolder(t, destRepo, dest); got != "" {
		t.Errorf("busy_job_id after cancel = %q, want released", got)
	}
	// Another job's gate is untouched.
	if got := busyHolder(t, destRepo, other); got != otherHolder.ID.String() {
		t.Errorf("unrelated busy_job_id = %q, want %q", got, otherHolder.ID)
	}
}

func TestReleaseStaleBusy(t *testing.T) {
	gdb := newTestDB(t)
	destRepo := NewDestinationRepository(gdb)
	jobRepo := NewJobRepository(gdb)
	agentRepo := NewAgentRepository(gdb)
	ctx := context.Background()

	stale, staleHolder := busyFixture(t, destRepo, jobRepo, agentRepo, "stale")
	setJobStatus(t, jobRepo, staleHolder, "cancelled")
	active, activeHolder := busyFixture(t, destRepo, jobRepo, agentRepo, "active")

	n, err := destRepo.ReleaseStaleBusy(ctx)
	if err != nil {
		t.Fatalf("ReleaseStaleBusy: %v", err)
	}
	if n != 1 {
		t.Errorf("released %d gates, want 1", n)
	}
	if got := busyHolder(t, destRepo, stale); got != "" {
		t.Errorf("stale busy_job_id = %q, want released", got)
	}
	if got := busyHolder(t, destRepo, active); got != activeHolder.ID.String() {
		t.Errorf("active busy_job_id = %q, want %q (job still running)", got, activeHolder.ID)
	}
}
