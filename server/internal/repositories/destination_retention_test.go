package repositories

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// newTestAgent creates a minimal agent for tests in this file.
func newTestAgentForRetention(t *testing.T, agentRepo AgentRepository, name string) *db.Agent {
	t.Helper()
	a := &db.Agent{Name: name, Status: "offline", Labels: "{}"}
	if err := agentRepo.Create(context.Background(), a); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return a
}

func TestListPoliciesByDestination(t *testing.T) {
	gdb := newTestDB(t)
	destRepo := NewDestinationRepository(gdb)
	policyRepo := NewPolicyRepository(gdb)
	agentRepo := NewAgentRepository(gdb)
	ctx := context.Background()

	agent := newTestAgentForRetention(t, agentRepo, "agent")
	dest := &db.Destination{Name: "shared", Type: "local", Config: "{}", Enabled: true}
	if err := destRepo.Create(ctx, dest); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	other := &db.Destination{Name: "unrelated", Type: "local", Config: "{}", Enabled: true}
	if err := destRepo.Create(ctx, other); err != nil {
		t.Fatalf("create other destination: %v", err)
	}

	p1 := &db.Policy{Name: "p1", AgentID: agent.ID, Schedule: "@daily", Sources: `["/data"]`}
	p2 := &db.Policy{Name: "p2", AgentID: agent.ID, Schedule: "@daily", Sources: `["/data"]`}
	for _, p := range []*db.Policy{p1, p2} {
		if err := policyRepo.Create(ctx, p); err != nil {
			t.Fatalf("create policy: %v", err)
		}
	}
	if err := policyRepo.AddDestination(ctx, &db.PolicyDestination{PolicyID: p1.ID, DestinationID: dest.ID}); err != nil {
		t.Fatalf("attach p1: %v", err)
	}
	if err := policyRepo.AddDestination(ctx, &db.PolicyDestination{PolicyID: p2.ID, DestinationID: dest.ID}); err != nil {
		t.Fatalf("attach p2: %v", err)
	}

	got, err := destRepo.ListPoliciesByDestination(ctx, dest.ID)
	if err != nil {
		t.Fatalf("ListPoliciesByDestination: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}

	gotUnrelated, err := destRepo.ListPoliciesByDestination(ctx, other.ID)
	if err != nil {
		t.Fatalf("ListPoliciesByDestination (unrelated): %v", err)
	}
	if len(gotUnrelated) != 0 {
		t.Errorf("len(gotUnrelated) = %d, want 0", len(gotUnrelated))
	}

	// A deleted policy must not be returned.
	if err := policyRepo.Delete(ctx, p2.ID); err != nil {
		t.Fatalf("delete p2: %v", err)
	}
	got, err = destRepo.ListPoliciesByDestination(ctx, dest.ID)
	if err != nil {
		t.Fatalf("ListPoliciesByDestination after delete: %v", err)
	}
	if len(got) != 1 || got[0].ID != p1.ID {
		t.Errorf("ListPoliciesByDestination after delete = %+v, want only p1", got)
	}
}

func TestPolicyCountsByDestination(t *testing.T) {
	gdb := newTestDB(t)
	destRepo := NewDestinationRepository(gdb)
	policyRepo := NewPolicyRepository(gdb)
	agentRepo := NewAgentRepository(gdb)
	ctx := context.Background()

	agent := newTestAgentForRetention(t, agentRepo, "agent")
	shared := &db.Destination{Name: "shared", Type: "local", Config: "{}", Enabled: true}
	solo := &db.Destination{Name: "solo", Type: "local", Config: "{}", Enabled: true}
	unused := &db.Destination{Name: "unused", Type: "local", Config: "{}", Enabled: true}
	for _, d := range []*db.Destination{shared, solo, unused} {
		if err := destRepo.Create(ctx, d); err != nil {
			t.Fatalf("create destination: %v", err)
		}
	}

	p1 := &db.Policy{Name: "p1", AgentID: agent.ID, Schedule: "@daily", Sources: `["/data"]`}
	p2 := &db.Policy{Name: "p2", AgentID: agent.ID, Schedule: "@daily", Sources: `["/data"]`}
	p3 := &db.Policy{Name: "p3", AgentID: agent.ID, Schedule: "@daily", Sources: `["/data"]`}
	for _, p := range []*db.Policy{p1, p2, p3} {
		if err := policyRepo.Create(ctx, p); err != nil {
			t.Fatalf("create policy: %v", err)
		}
	}
	if err := policyRepo.AddDestination(ctx, &db.PolicyDestination{PolicyID: p1.ID, DestinationID: shared.ID}); err != nil {
		t.Fatalf("attach p1 to shared: %v", err)
	}
	if err := policyRepo.AddDestination(ctx, &db.PolicyDestination{PolicyID: p2.ID, DestinationID: shared.ID}); err != nil {
		t.Fatalf("attach p2 to shared: %v", err)
	}
	if err := policyRepo.AddDestination(ctx, &db.PolicyDestination{PolicyID: p3.ID, DestinationID: solo.ID}); err != nil {
		t.Fatalf("attach p3 to solo: %v", err)
	}

	counts, err := destRepo.PolicyCountsByDestination(ctx)
	if err != nil {
		t.Fatalf("PolicyCountsByDestination: %v", err)
	}
	if counts[shared.ID] != 2 {
		t.Errorf("counts[shared] = %d, want 2", counts[shared.ID])
	}
	if counts[solo.ID] != 1 {
		t.Errorf("counts[solo] = %d, want 1", counts[solo.ID])
	}
	if _, ok := counts[unused.ID]; ok {
		t.Errorf("counts[unused] present = %d, want absent (no live policies)", counts[unused.ID])
	}
}

func TestListWithRetentionSchedule(t *testing.T) {
	gdb := newTestDB(t)
	destRepo := NewDestinationRepository(gdb)
	ctx := context.Background()

	eligible := &db.Destination{
		Name: "eligible", Type: "local", Config: "{}", Enabled: true,
		RetentionEnabled: true, RetentionSchedule: "0 3 * * *",
	}
	disabled := &db.Destination{
		Name: "disabled", Type: "local", Config: "{}", Enabled: true,
		RetentionEnabled: false, RetentionSchedule: "0 3 * * *",
	}
	appendOnly := &db.Destination{
		Name: "append-only", Type: "local", Config: "{}", Enabled: true,
		RetentionEnabled: true, RetentionSchedule: "0 3 * * *", AppendOnly: true,
	}
	noSchedule := &db.Destination{
		Name: "no-schedule", Type: "local", Config: "{}", Enabled: true,
		RetentionEnabled: true, RetentionSchedule: "",
	}
	for _, d := range []*db.Destination{eligible, disabled, appendOnly, noSchedule} {
		if err := destRepo.Create(ctx, d); err != nil {
			t.Fatalf("create destination %q: %v", d.Name, err)
		}
	}

	got, err := destRepo.ListWithRetentionSchedule(ctx)
	if err != nil {
		t.Fatalf("ListWithRetentionSchedule: %v", err)
	}
	if len(got) != 1 || got[0].ID != eligible.ID {
		t.Errorf("ListWithRetentionSchedule = %+v, want only %q", got, eligible.Name)
	}
}

func TestTryAcquireBusy_SecondCallFails(t *testing.T) {
	gdb := newTestDB(t)
	destRepo := NewDestinationRepository(gdb)
	jobRepo := NewJobRepository(gdb)
	agentRepo := NewAgentRepository(gdb)
	ctx := context.Background()

	agent := newTestAgentForRetention(t, agentRepo, "agent")
	dest := &db.Destination{Name: "d", Type: "local", Config: "{}", Enabled: true}
	if err := destRepo.Create(ctx, dest); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	jobA := &db.Job{AgentID: agent.ID, Type: "backup", Status: "pending"}
	jobB := &db.Job{AgentID: agent.ID, Type: "retention", Status: "pending"}
	for _, j := range []*db.Job{jobA, jobB} {
		if err := jobRepo.Create(ctx, j); err != nil {
			t.Fatalf("create job: %v", err)
		}
	}

	acquired, err := destRepo.TryAcquireBusy(ctx, dest.ID, jobA.ID)
	if err != nil {
		t.Fatalf("TryAcquireBusy (jobA): %v", err)
	}
	if !acquired {
		t.Fatal("TryAcquireBusy (jobA) = false, want true (destination was free)")
	}

	acquired, err = destRepo.TryAcquireBusy(ctx, dest.ID, jobB.ID)
	if err != nil {
		t.Fatalf("TryAcquireBusy (jobB): %v", err)
	}
	if acquired {
		t.Error("TryAcquireBusy (jobB) = true, want false (destination held by jobA)")
	}

	// Idempotent for the same job — a retried dispatch must not treat its own
	// hold as contention.
	acquired, err = destRepo.TryAcquireBusy(ctx, dest.ID, jobA.ID)
	if err != nil {
		t.Fatalf("TryAcquireBusy (jobA again): %v", err)
	}
	if !acquired {
		t.Error("TryAcquireBusy (jobA again) = false, want true (idempotent re-acquire by the same job)")
	}

	// After release, another job can acquire it.
	if err := destRepo.ReleaseBusy(ctx, dest.ID, jobA.ID); err != nil {
		t.Fatalf("ReleaseBusy: %v", err)
	}
	acquired, err = destRepo.TryAcquireBusy(ctx, dest.ID, jobB.ID)
	if err != nil {
		t.Fatalf("TryAcquireBusy (jobB after release): %v", err)
	}
	if !acquired {
		t.Error("TryAcquireBusy (jobB after release) = false, want true")
	}
}

func TestReleaseBusy_WrongJobIDNoop(t *testing.T) {
	gdb := newTestDB(t)
	destRepo := NewDestinationRepository(gdb)
	jobRepo := NewJobRepository(gdb)
	agentRepo := NewAgentRepository(gdb)
	ctx := context.Background()

	agent := newTestAgentForRetention(t, agentRepo, "agent")
	dest := &db.Destination{Name: "d", Type: "local", Config: "{}", Enabled: true}
	if err := destRepo.Create(ctx, dest); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	jobA := &db.Job{AgentID: agent.ID, Type: "backup", Status: "pending"}
	jobB := &db.Job{AgentID: agent.ID, Type: "retention", Status: "pending"}
	for _, j := range []*db.Job{jobA, jobB} {
		if err := jobRepo.Create(ctx, j); err != nil {
			t.Fatalf("create job: %v", err)
		}
	}

	if acquired, err := destRepo.TryAcquireBusy(ctx, dest.ID, jobA.ID); err != nil || !acquired {
		t.Fatalf("TryAcquireBusy (jobA): acquired=%v err=%v", acquired, err)
	}

	// A stale release from a job that never held the gate must not clear it.
	if err := destRepo.ReleaseBusy(ctx, dest.ID, jobB.ID); err != nil {
		t.Fatalf("ReleaseBusy (wrong job): %v", err)
	}
	acquired, err := destRepo.TryAcquireBusy(ctx, dest.ID, jobB.ID)
	if err != nil {
		t.Fatalf("TryAcquireBusy (jobB): %v", err)
	}
	if acquired {
		t.Error("TryAcquireBusy (jobB) = true after a no-op release, want false — jobA should still hold the gate")
	}
}

func TestReleaseBusyForJobs(t *testing.T) {
	gdb := newTestDB(t)
	destRepo := NewDestinationRepository(gdb)
	jobRepo := NewJobRepository(gdb)
	agentRepo := NewAgentRepository(gdb)
	ctx := context.Background()

	agent := newTestAgentForRetention(t, agentRepo, "agent")
	d1 := &db.Destination{Name: "d1", Type: "local", Config: "{}", Enabled: true}
	d2 := &db.Destination{Name: "d2", Type: "local", Config: "{}", Enabled: true}
	for _, d := range []*db.Destination{d1, d2} {
		if err := destRepo.Create(ctx, d); err != nil {
			t.Fatalf("create destination: %v", err)
		}
	}
	j1 := &db.Job{AgentID: agent.ID, Type: "backup", Status: "running"}
	j2 := &db.Job{AgentID: agent.ID, Type: "retention", Status: "running"}
	for _, j := range []*db.Job{j1, j2} {
		if err := jobRepo.Create(ctx, j); err != nil {
			t.Fatalf("create job: %v", err)
		}
	}
	if acquired, err := destRepo.TryAcquireBusy(ctx, d1.ID, j1.ID); err != nil || !acquired {
		t.Fatalf("TryAcquireBusy d1/j1: acquired=%v err=%v", acquired, err)
	}
	if acquired, err := destRepo.TryAcquireBusy(ctx, d2.ID, j2.ID); err != nil || !acquired {
		t.Fatalf("TryAcquireBusy d2/j2: acquired=%v err=%v", acquired, err)
	}

	if err := destRepo.ReleaseBusyForJobs(ctx, []uuid.UUID{j1.ID, j2.ID}); err != nil {
		t.Fatalf("ReleaseBusyForJobs: %v", err)
	}

	for _, d := range []*db.Destination{d1, d2} {
		got, err := destRepo.GetByID(ctx, d.ID)
		if err != nil {
			t.Fatalf("GetByID %s: %v", d.Name, err)
		}
		if got.BusyJobID != nil {
			t.Errorf("destination %q BusyJobID = %v, want nil after ReleaseBusyForJobs", d.Name, got.BusyJobID)
		}
	}
}
