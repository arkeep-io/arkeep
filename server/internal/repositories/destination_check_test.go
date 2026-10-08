package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

func TestListWithCheckSchedule(t *testing.T) {
	gdb := newTestDB(t)
	destRepo := NewDestinationRepository(gdb)
	ctx := context.Background()

	eligible := &db.Destination{
		Name: "eligible", Type: "local", Config: "{}", Enabled: true,
		CheckEnabled: true, CheckSchedule: "0 3 * * 0",
	}
	// restic check only reads the repository, so append-only is no obstacle.
	appendOnly := &db.Destination{
		Name: "append-only", Type: "local", Config: "{}", Enabled: true,
		CheckEnabled: true, CheckSchedule: "0 3 * * 0", AppendOnly: true,
	}
	disabled := &db.Destination{
		Name: "disabled", Type: "local", Config: "{}", Enabled: true,
		CheckEnabled: false, CheckSchedule: "0 3 * * 0",
	}
	noSchedule := &db.Destination{
		Name: "no-schedule", Type: "local", Config: "{}", Enabled: true,
		CheckEnabled: true, CheckSchedule: "",
	}
	for _, d := range []*db.Destination{eligible, appendOnly, disabled, noSchedule} {
		if err := destRepo.Create(ctx, d); err != nil {
			t.Fatalf("create destination %q: %v", d.Name, err)
		}
	}

	got, err := destRepo.ListWithCheckSchedule(ctx)
	if err != nil {
		t.Fatalf("ListWithCheckSchedule: %v", err)
	}
	ids := map[uuid.UUID]bool{}
	for _, d := range got {
		ids[d.ID] = true
	}
	if len(got) != 2 || !ids[eligible.ID] || !ids[appendOnly.ID] {
		t.Errorf("ListWithCheckSchedule = %+v, want %q and %q", got, eligible.Name, appendOnly.Name)
	}
}

func TestUpdateLastCheck(t *testing.T) {
	gdb := newTestDB(t)
	destRepo := NewDestinationRepository(gdb)
	ctx := context.Background()

	dest := &db.Destination{Name: "d", Type: "local", Config: "{}", Enabled: true}
	if err := destRepo.Create(ctx, dest); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	jobID := uuid.Must(uuid.NewV7())
	at := time.Now().UTC().Truncate(time.Second)
	if err := destRepo.UpdateLastCheck(ctx, dest.ID, jobID, "failed", at); err != nil {
		t.Fatalf("UpdateLastCheck: %v", err)
	}

	got, err := destRepo.GetByID(ctx, dest.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.LastCheckStatus != "failed" {
		t.Errorf("LastCheckStatus = %q, want \"failed\"", got.LastCheckStatus)
	}
	if got.LastCheckJobID == nil || *got.LastCheckJobID != jobID {
		t.Errorf("LastCheckJobID = %v, want %v", got.LastCheckJobID, jobID)
	}
	if got.LastCheckAt == nil || !got.LastCheckAt.Equal(at) {
		t.Errorf("LastCheckAt = %v, want %v", got.LastCheckAt, at)
	}
}

func TestDashboardCheckCounts(t *testing.T) {
	gdb := newTestDB(t)
	destRepo := NewDestinationRepository(gdb)
	dashRepo := NewDashboardRepository(gdb)
	ctx := context.Background()

	create := func(name string, enabled bool, lastStatus string) {
		t.Helper()
		d := &db.Destination{Name: name, Type: "local", Config: "{}", Enabled: true, CheckEnabled: enabled}
		if err := destRepo.Create(ctx, d); err != nil {
			t.Fatalf("create destination %q: %v", name, err)
		}
		if lastStatus != "" {
			if err := destRepo.UpdateLastCheck(ctx, d.ID, uuid.Must(uuid.NewV7()), lastStatus, time.Now().UTC()); err != nil {
				t.Fatalf("UpdateLastCheck %q: %v", name, err)
			}
		}
	}
	create("ok", true, "succeeded")
	create("broken", true, "failed")
	create("never", true, "")
	create("disabled-broken", false, "failed")

	stats, err := dashRepo.GetStats(ctx)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.ChecksEnabled != 3 || stats.ChecksFailed != 1 {
		t.Errorf("checks (enabled, failed) = (%d, %d), want (3, 1)", stats.ChecksEnabled, stats.ChecksFailed)
	}
}
