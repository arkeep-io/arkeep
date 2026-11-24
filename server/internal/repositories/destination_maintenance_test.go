package repositories

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

type maintenanceEnv struct {
	dests  DestinationRepository
	jobs   JobRepository
	agents AgentRepository
	agent  *db.Agent
	dest   *db.Destination
}

func newMaintenanceEnv(t *testing.T) *maintenanceEnv {
	t.Helper()
	gdb := newTestDB(t)
	e := &maintenanceEnv{dests: NewDestinationRepository(gdb), jobs: NewJobRepository(gdb), agents: NewAgentRepository(gdb)}
	e.agent = newTestAgentForRetention(t, e.agents, "maintenance-agent")
	e.dest = &db.Destination{Name: "d-maint", Type: "local", Config: "{}", Enabled: true}
	if err := e.dests.Create(context.Background(), e.dest); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	return e
}

// job creates a job of jobType in status against the destination.
func (e *maintenanceEnv) job(t *testing.T, jobType, status string) *db.Job {
	t.Helper()
	ctx := context.Background()
	job := &db.Job{AgentID: e.agent.ID, Type: jobType, Status: status}
	if err := e.jobs.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := e.jobs.CreateDestination(ctx, &db.JobDestination{JobID: job.ID, DestinationID: e.dest.ID, Status: "pending"}); err != nil {
		t.Fatalf("create job destination: %v", err)
	}
	return job
}

func (e *maintenanceEnv) claim(t *testing.T, mode string) bool {
	t.Helper()
	j := e.job(t, "backup", "pending")
	ok, err := e.dests.TryAcquireBusyAll(context.Background(), []uuid.UUID{e.dest.ID}, j.ID, mode)
	if err != nil {
		t.Fatalf("TryAcquireBusyAll: %v", err)
	}
	return ok
}

func (e *maintenanceEnv) reload(t *testing.T) *db.Destination {
	t.Helper()
	d, err := e.dests.GetByID(context.Background(), e.dest.ID)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestMaintenance_BlocksEveryClaim: no job, shared or exclusive, can claim a
// destination in maintenance; it waits until the maintenance ends.
func TestMaintenance_BlocksEveryClaim(t *testing.T) {
	e := newMaintenanceEnv(t)
	ctx := context.Background()

	if err := e.dests.StartMaintenance(ctx, e.dest.ID, "admin@example.com"); err != nil {
		t.Fatalf("StartMaintenance: %v", err)
	}
	d := e.reload(t)
	if d.MaintenanceSince == nil || d.MaintenanceBy != "admin@example.com" || d.MaintenanceJobID != nil {
		t.Fatalf("maintenance = (%v, %q, %v), want started by the admin with no job", d.MaintenanceSince, d.MaintenanceBy, d.MaintenanceJobID)
	}
	if e.claim(t, db.LockShared) || e.claim(t, db.LockExclusive) {
		t.Fatal("a job claimed a destination in maintenance")
	}

	if err := e.dests.EndMaintenance(ctx, e.dest.ID); err != nil {
		t.Fatalf("EndMaintenance: %v", err)
	}
	if d := e.reload(t); d.MaintenanceSince != nil || d.MaintenanceBy != "" {
		t.Errorf("maintenance still set after EndMaintenance: %v %q", d.MaintenanceSince, d.MaintenanceBy)
	}
	if !e.claim(t, db.LockShared) {
		t.Error("the destination could not be claimed once the maintenance ended")
	}
}

// TestClaimForRepair_RefusesABusyDestination: a repair is refused while a job
// holds the destination, and while another repair is in progress.
func TestClaimForRepair_RefusesABusyDestination(t *testing.T) {
	e := newMaintenanceEnv(t)
	ctx := context.Background()

	if !e.claim(t, db.LockShared) {
		t.Fatal("setup: the backup could not claim the destination")
	}
	repair := e.job(t, "repair", "pending")
	if _, err := e.dests.ClaimForRepair(ctx, e.dest.ID, repair.ID, "admin@example.com"); !errors.Is(err, ErrDestinationBusy) {
		t.Fatalf("ClaimForRepair on a busy destination = %v, want ErrDestinationBusy", err)
	}
	if d := e.reload(t); d.MaintenanceSince != nil {
		t.Error("a refused repair left the destination in maintenance")
	}

	other := newMaintenanceEnv(t)
	first := other.job(t, "repair", "running")
	if _, err := other.dests.ClaimForRepair(ctx, other.dest.ID, first.ID, "a@example.com"); err != nil {
		t.Fatalf("first ClaimForRepair: %v", err)
	}
	second := other.job(t, "repair", "pending")
	if _, err := other.dests.ClaimForRepair(ctx, other.dest.ID, second.ID, "a@example.com"); !errors.Is(err, ErrRepairInProgress) {
		t.Errorf("second ClaimForRepair = %v, want ErrRepairInProgress", err)
	}
}

// TestClaimForRepair_MaintenanceEndsWithTheRepair: a repair on a destination
// that is not in maintenance puts it in maintenance for the job's duration.
// The maintenance ends with the job, however the job ends.
func TestClaimForRepair_MaintenanceEndsWithTheRepair(t *testing.T) {
	for _, end := range []string{"succeeded", "failed", "cancelled", "interrupted"} {
		t.Run(end, func(t *testing.T) {
			e := newMaintenanceEnv(t)
			ctx := context.Background()
			repair := e.job(t, "repair", "pending")

			auto, err := e.dests.ClaimForRepair(ctx, e.dest.ID, repair.ID, "admin@example.com")
			if err != nil || !auto {
				t.Fatalf("ClaimForRepair = (%v, %v), want an automatic maintenance", auto, err)
			}
			if d := e.reload(t); d.MaintenanceJobID == nil || *d.MaintenanceJobID != repair.ID {
				t.Fatalf("maintenance job = %v, want %s", d.MaintenanceJobID, repair.ID)
			}
			if e.claim(t, db.LockShared) {
				t.Fatal("a backup claimed the destination during the repair")
			}
			if err := e.dests.EndMaintenance(ctx, e.dest.ID); !errors.Is(err, ErrRepairInProgress) {
				t.Errorf("EndMaintenance during the repair = %v, want ErrRepairInProgress", err)
			}

			setJobStatus(t, e.jobs, repair, end)
			if !e.claim(t, db.LockShared) {
				t.Fatalf("the destination stayed in maintenance after the repair %s", end)
			}
			if n, err := e.dests.ClearEndedMaintenance(ctx); err != nil || n != 1 {
				t.Errorf("ClearEndedMaintenance = (%d, %v), want 1 destination cleared", n, err)
			}
			if d := e.reload(t); d.MaintenanceSince != nil || d.MaintenanceJobID != nil {
				t.Error("the maintenance columns were not cleared")
			}
		})
	}
}

// TestClaimForRepair_InAnAdminMaintenance: a repair under an admin's
// maintenance does not end it, and starting a maintenance during an automatic
// one makes it the admin's, so it outlives the repair.
func TestClaimForRepair_InAnAdminMaintenance(t *testing.T) {
	e := newMaintenanceEnv(t)
	ctx := context.Background()
	if err := e.dests.StartMaintenance(ctx, e.dest.ID, "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	repair := e.job(t, "repair", "pending")
	if auto, err := e.dests.ClaimForRepair(ctx, e.dest.ID, repair.ID, "other@example.com"); err != nil || auto {
		t.Fatalf("ClaimForRepair = (%v, %v), want the existing maintenance", auto, err)
	}
	setJobStatus(t, e.jobs, repair, "succeeded")
	if e.claim(t, db.LockShared) {
		t.Error("the admin's maintenance ended with the repair")
	}

	f := newMaintenanceEnv(t)
	auto := f.job(t, "repair", "running")
	if _, err := f.dests.ClaimForRepair(ctx, f.dest.ID, auto.ID, "a@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := f.dests.StartMaintenance(ctx, f.dest.ID, "b@example.com"); err != nil {
		t.Fatal(err)
	}
	setJobStatus(t, f.jobs, auto, "succeeded")
	if f.claim(t, db.LockShared) {
		t.Error("a maintenance an admin took over ended with the repair")
	}
}

func TestMaintenance_MissingDestination(t *testing.T) {
	e := newMaintenanceEnv(t)
	ctx := context.Background()
	if err := e.dests.StartMaintenance(ctx, uuid.New(), "a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("StartMaintenance on a missing destination = %v, want ErrNotFound", err)
	}
	if _, err := e.dests.ClaimForRepair(ctx, uuid.New(), uuid.New(), "a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ClaimForRepair on a missing destination = %v, want ErrNotFound", err)
	}
}
