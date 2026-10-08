package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

func idsOf(dests ...*db.Destination) []uuid.UUID {
	ids := make([]uuid.UUID, len(dests))
	for i, d := range dests {
		ids[i] = d.ID
	}
	return ids
}

func jobIDs(jobs []db.Job) []uuid.UUID {
	ids := make([]uuid.UUID, len(jobs))
	for i, j := range jobs {
		ids[i] = j.ID
	}
	return ids
}

func TestTryAcquireBusyAll(t *testing.T) {
	gdb := newTestDB(t)
	destRepo := NewDestinationRepository(gdb)
	jobRepo := NewJobRepository(gdb)
	agentRepo := NewAgentRepository(gdb)
	ctx := context.Background()

	busy, holder := busyFixture(t, destRepo, jobRepo, agentRepo, "busy")
	free := &db.Destination{Name: "d-free", Type: "local", Config: "{}", Enabled: true}
	if err := destRepo.Create(ctx, free); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	job := &db.Job{AgentID: holder.AgentID, Type: "backup", Status: "pending"}
	if err := jobRepo.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	// One destination busy: nothing is claimed, not even the free one.
	acquired, err := destRepo.TryAcquireBusyAll(ctx, nil, job.ID)
	if err != nil || !acquired {
		t.Fatalf("TryAcquireBusyAll(no destinations) = %v, %v; want true, nil", acquired, err)
	}
	acquired, err = destRepo.TryAcquireBusyAll(ctx, idsOf(free, busy), job.ID)
	if err != nil {
		t.Fatalf("TryAcquireBusyAll: %v", err)
	}
	if acquired {
		t.Fatal("TryAcquireBusyAll with a busy destination = true, want false")
	}
	if got := busyHolder(t, destRepo, free); got != "" {
		t.Errorf("free destination holder = %q, want none: a refused claim must roll back", got)
	}
	if got := busyHolder(t, destRepo, busy); got != holder.ID.String() {
		t.Errorf("busy destination holder = %q, want %s", got, holder.ID)
	}

	// Holder done: both are claimed, and claiming again is idempotent.
	if err := jobRepo.UpdateStatus(ctx, holder.ID, "succeeded", nil, nil, ""); err != nil {
		t.Fatalf("finish holder: %v", err)
	}
	for range 2 {
		acquired, err = destRepo.TryAcquireBusyAll(ctx, idsOf(free, busy, free), job.ID)
		if err != nil || !acquired {
			t.Fatalf("TryAcquireBusyAll = %v, %v; want true, nil", acquired, err)
		}
	}
	for _, d := range []*db.Destination{free, busy} {
		if got := busyHolder(t, destRepo, d); got != job.ID.String() {
			t.Errorf("destination %s holder = %q, want %s", d.Name, got, job.ID)
		}
	}
}

func TestDestinationQueueQueries(t *testing.T) {
	gdb := newTestDB(t)
	destRepo := NewDestinationRepository(gdb)
	jobRepo := NewJobRepository(gdb)
	agentRepo := NewAgentRepository(gdb)
	ctx := context.Background()

	agent := newTestAgentForRetention(t, agentRepo, "agent")
	a := &db.Destination{Name: "a", Type: "local", Config: "{}", Enabled: true}
	b := &db.Destination{Name: "b", Type: "local", Config: "{}", Enabled: true}
	for _, d := range []*db.Destination{a, b} {
		if err := destRepo.Create(ctx, d); err != nil {
			t.Fatalf("create destination: %v", err)
		}
	}
	policy := &db.Policy{Name: "p", AgentID: agent.ID, Schedule: "@daily", Sources: "[]", RepoPassword: db.EncryptedString("x")}
	if err := NewPolicyRepository(gdb).Create(ctx, policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	newJob := func(jobType, status string, destStatus map[*db.Destination]string) *db.Job {
		t.Helper()
		j := &db.Job{AgentID: agent.ID, Type: jobType, Status: "pending"}
		if jobType == "backup" {
			j.PolicyID = &policy.ID
		}
		if err := jobRepo.Create(ctx, j); err != nil {
			t.Fatalf("create job: %v", err)
		}
		setJobStatus(t, jobRepo, j, status)
		for d, st := range destStatus {
			if err := jobRepo.CreateDestination(ctx, &db.JobDestination{JobID: j.ID, DestinationID: d.ID, Status: st}); err != nil {
				t.Fatalf("create job destination: %v", err)
			}
		}
		time.Sleep(2 * time.Millisecond) // distinct created_at for the FIFO order
		return j
	}

	if has, _ := jobRepo.HasPendingJob(ctx, policy.ID); has {
		t.Fatal("HasPendingJob before any job = true")
	}
	older := newJob("backup", "waiting", map[*db.Destination]string{a: "pending", b: "skipped"})
	younger := newJob("backup", "waiting", map[*db.Destination]string{b: "pending"})
	newJob("backup", "running", map[*db.Destination]string{a: "pending"})

	if has, err := jobRepo.HasPendingJob(ctx, policy.ID); err != nil || !has {
		t.Errorf("HasPendingJob with a waiting job = %v, %v; want true: a waiting job must coalesce scheduled ticks", has, err)
	}

	waiting, err := jobRepo.ListWaiting(ctx, 10)
	if err != nil {
		t.Fatalf("ListWaiting: %v", err)
	}
	if len(waiting) != 2 || waiting[0].ID != older.ID || waiting[1].ID != younger.ID {
		t.Errorf("ListWaiting = %v, want [older, younger]", jobIDs(waiting))
	}

	ids, err := jobRepo.ListQueuedDestinationIDs(ctx, older.ID)
	if err != nil {
		t.Fatalf("ListQueuedDestinationIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != a.ID {
		t.Errorf("ListQueuedDestinationIDs = %v, want only the unresolved destination %s", ids, a.ID)
	}

	for _, tc := range []struct {
		name    string
		dests   []*db.Destination
		exclude *db.Job
		want    bool
	}{
		{"a needed by older", []*db.Destination{a}, younger, true},
		{"b only skipped by older", []*db.Destination{b}, younger, false},
		{"b needed by younger", []*db.Destination{b}, older, true},
		{"own rows excluded", []*db.Destination{a}, older, false},
	} {
		got, err := jobRepo.HasWaitingForDestinations(ctx, idsOf(tc.dests...), tc.exclude.ID)
		if err != nil {
			t.Fatalf("%s: HasWaitingForDestinations: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: HasWaitingForDestinations = %v, want %v", tc.name, got, tc.want)
		}
	}

	if has, _ := jobRepo.HasActiveJobOfType(ctx, "retention", a.ID); has {
		t.Error("HasActiveJobOfType(retention) with only backups = true, want false")
	}
	newJob("retention", "succeeded", map[*db.Destination]string{a: "succeeded"})
	if has, _ := jobRepo.HasActiveJobOfType(ctx, "retention", a.ID); has {
		t.Error("HasActiveJobOfType(retention) with a finished sweep = true, want false")
	}
	newJob("retention", "waiting", map[*db.Destination]string{a: "pending"})
	if has, err := jobRepo.HasActiveJobOfType(ctx, "retention", a.ID); err != nil || !has {
		t.Errorf("HasActiveJobOfType(retention) with a waiting sweep = %v, %v; want true", has, err)
	}
	if has, _ := jobRepo.HasActiveJobOfType(ctx, "retention", b.ID); has {
		t.Error("HasActiveJobOfType(retention) on another destination = true, want false")
	}
	if has, _ := jobRepo.HasActiveJobOfType(ctx, "check", a.ID); has {
		t.Error("HasActiveJobOfType(check) with only a waiting sweep = true, want false")
	}
}
