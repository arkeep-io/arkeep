package scheduler

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// newTestScheduler builds a Scheduler over a fresh in-memory SQLite database with
// all migrations applied. No agent is connected, so dispatch always fails — which
// is deliberate: dispatch failure is non-fatal and leaves the job pending, so the
// tests can assert on what was persisted without simulating an agent.
func newTestScheduler(t *testing.T) (*Scheduler, *gorm.DB, repositories.PolicyRepository, repositories.JobRepository) {
	t.Helper()
	if err := db.InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("db.InitEncryption: %v", err)
	}
	gdb, err := db.New(db.Config{
		Driver:   "sqlite",
		DSN:      ":memory:",
		Logger:   zap.NewNop(),
		LogLevel: gormlogger.Silent,
	})
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}

	policies := repositories.NewPolicyRepository(gdb)
	jobs := repositories.NewJobRepository(gdb)
	dests := repositories.NewDestinationRepository(gdb)

	s, err := New(policies, jobs, dests, agentmanager.New(zap.NewNop()), zap.NewNop())
	if err != nil {
		t.Fatalf("scheduler.New: %v", err)
	}
	return s, gdb, policies, jobs
}

// resumeFixture is one agent + policy + interrupted backup job, the starting
// point of every resume scenario.
type resumeFixture struct {
	agentID uuid.UUID
	policy  *db.Policy
	job     *db.Job
}

func newResumeFixture(t *testing.T, gdb *gorm.DB, policies repositories.PolicyRepository, jobs repositories.JobRepository) *resumeFixture {
	t.Helper()
	ctx := context.Background()

	agent := &db.Agent{Name: "test-agent", Status: "online", Labels: "{}"}
	if err := repositories.NewAgentRepository(gdb).Create(ctx, agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	policy := &db.Policy{
		Name:    "laptop-policy",
		AgentID: agent.ID,
		// Set explicitly: db.Policy.ResumeInterrupted carries no GORM default tag
		// (see the field comment), so the Go zero value is what gets inserted.
		ResumeInterrupted: true,
		Schedule:          "0 2 * * *",
		Enabled:           true,
		Sources:           `["/data"]`,
		RepoPassword:      db.EncryptedString("repo-secret"),
	}
	if err := policies.Create(ctx, policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	job := &db.Job{
		PolicyID: &policy.ID,
		AgentID:  agent.ID,
		Type:     "backup",
		Status:   "pending",
	}
	if err := jobs.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	// Status and resume_attempt are set with a direct update: Create would skip
	// the zero value and the CHECK constraint is what we want to exercise anyway.
	if err := gdb.Model(job).Updates(map[string]any{"status": "interrupted", "error": "agent disconnected"}).Error; err != nil {
		t.Fatalf("mark job interrupted: %v", err)
	}

	return &resumeFixture{agentID: agent.ID, policy: policy, job: job}
}

// jobsForPolicy returns every job of a policy, oldest first.
func jobsForPolicy(t *testing.T, gdb *gorm.DB, policyID uuid.UUID) []db.Job {
	t.Helper()
	var out []db.Job
	if err := gdb.Where("policy_id = ?", policyID).Order("created_at ASC").Find(&out).Error; err != nil {
		t.Fatalf("load jobs: %v", err)
	}
	return out
}

func TestResumeInterrupted_CreatesFollowUpJob(t *testing.T) {
	s, gdb, policies, jobs := newTestScheduler(t)
	f := newResumeFixture(t, gdb, policies, jobs)
	ctx := context.Background()

	s.ResumeInterrupted(ctx, f.agentID)

	all := jobsForPolicy(t, gdb, f.policy.ID)
	if len(all) != 2 {
		t.Fatalf("policy has %d jobs after resume, want 2 (the interrupted one plus its follow-up)", len(all))
	}

	// The interrupted job keeps its own record.
	if all[0].ID != f.job.ID {
		t.Fatalf("first job is %s, want the interrupted job %s", all[0].ID, f.job.ID)
	}
	if all[0].Status != "interrupted" {
		t.Errorf("interrupted job status = %q, want it left as \"interrupted\"", all[0].Status)
	}

	resumed := all[1]
	if resumed.ResumeOfJobID == nil || *resumed.ResumeOfJobID != f.job.ID {
		t.Errorf("resumed job ResumeOfJobID = %v, want %s", resumed.ResumeOfJobID, f.job.ID)
	}
	if resumed.ResumeAttempt != 1 {
		t.Errorf("resumed job ResumeAttempt = %d, want 1", resumed.ResumeAttempt)
	}
	if resumed.Type != "backup" {
		t.Errorf("resumed job Type = %q, want \"backup\"", resumed.Type)
	}

	// A resume continues an earlier scheduled run, so it must not move the schedule.
	stored, err := policies.GetByID(ctx, f.policy.ID)
	if err != nil {
		t.Fatalf("GetByID(policy): %v", err)
	}
	if stored.LastRunAt != nil {
		t.Errorf("policy LastRunAt = %v after a resume, want it left unset", stored.LastRunAt)
	}
}

func TestResumeInterrupted_Skips(t *testing.T) {
	tests := []struct {
		name string
		// setup mutates the fixture to create the condition under test.
		setup func(t *testing.T, gdb *gorm.DB, f *resumeFixture)
		// wantOriginalStatus is the status the interrupted job must end up in.
		wantOriginalStatus string
	}{
		{
			name: "policy is disabled",
			setup: func(t *testing.T, gdb *gorm.DB, f *resumeFixture) {
				if err := gdb.Model(f.policy).Update("enabled", false).Error; err != nil {
					t.Fatalf("disable policy: %v", err)
				}
			},
			wantOriginalStatus: "interrupted",
		},
		{
			name: "resume is disabled on the policy",
			setup: func(t *testing.T, gdb *gorm.DB, f *resumeFixture) {
				if err := gdb.Model(f.policy).Update("resume_interrupted", false).Error; err != nil {
					t.Fatalf("disable resume: %v", err)
				}
			},
			wantOriginalStatus: "interrupted",
		},
		{
			name: "a newer run of the policy already exists",
			setup: func(t *testing.T, gdb *gorm.DB, f *resumeFixture) {
				newer := &db.Job{
					PolicyID: &f.policy.ID,
					AgentID:  f.agentID,
					Type:     "backup",
					Status:   "succeeded",
				}
				if err := gdb.Create(newer).Error; err != nil {
					t.Fatalf("create newer job: %v", err)
				}
				if err := gdb.Model(newer).Update("created_at", f.job.CreatedAt.Add(time.Hour)).Error; err != nil {
					t.Fatalf("age newer job: %v", err)
				}
			},
			wantOriginalStatus: "interrupted",
		},
		{
			name: "the job is a restore, not a backup",
			setup: func(t *testing.T, gdb *gorm.DB, f *resumeFixture) {
				if err := gdb.Model(f.job).Update("type", "restore").Error; err != nil {
					t.Fatalf("change job type: %v", err)
				}
			},
			wantOriginalStatus: "interrupted",
		},
		{
			name: "resume attempts are exhausted",
			setup: func(t *testing.T, gdb *gorm.DB, f *resumeFixture) {
				if err := gdb.Model(f.job).Update("resume_attempt", maxResumeAttempts).Error; err != nil {
					t.Fatalf("exhaust attempts: %v", err)
				}
			},
			// Closed out as failed so it stops being scanned on every reconnect.
			wantOriginalStatus: "failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, gdb, policies, jobs := newTestScheduler(t)
			f := newResumeFixture(t, gdb, policies, jobs)
			tt.setup(t, gdb, f)

			s.ResumeInterrupted(context.Background(), f.agentID)

			all := jobsForPolicy(t, gdb, f.policy.ID)
			for _, j := range all {
				if j.ResumeOfJobID != nil {
					t.Errorf("a resume job was created (%s) but this case must not resume", j.ID)
				}
			}

			var original *db.Job
			for i := range all {
				if all[i].ID == f.job.ID {
					original = &all[i]
				}
			}
			if original == nil {
				t.Fatalf("the interrupted job disappeared")
			}
			if original.Status != tt.wantOriginalStatus {
				t.Errorf("interrupted job status = %q, want %q", original.Status, tt.wantOriginalStatus)
			}
		})
	}
}

// TestResumeInterrupted_ExhaustedIsReportedOnce guards against notifying on every
// single reconnection once resume has given up.
func TestResumeInterrupted_ExhaustedIsReportedOnce(t *testing.T) {
	s, gdb, policies, jobs := newTestScheduler(t)
	f := newResumeFixture(t, gdb, policies, jobs)
	if err := gdb.Model(f.job).Update("resume_attempt", maxResumeAttempts).Error; err != nil {
		t.Fatalf("exhaust attempts: %v", err)
	}
	notif := &countingNotifier{}
	s.SetNotificationService(notif)

	s.ResumeInterrupted(context.Background(), f.agentID)
	s.ResumeInterrupted(context.Background(), f.agentID)

	if notif.jobFailed != 1 {
		t.Errorf("NotifyJobFailed called %d times across two reconnections, want 1", notif.jobFailed)
	}
}

// countingNotifier is a notification.Service that only counts calls.
type countingNotifier struct {
	jobFailed int
}

func (c *countingNotifier) NotifyJobSucceeded(_ context.Context, _, _ uuid.UUID, _ string) error {
	return nil
}

func (c *countingNotifier) NotifyJobFailed(_ context.Context, _, _ uuid.UUID, _, _ string) error {
	c.jobFailed++
	return nil
}

func (c *countingNotifier) NotifyAgentOffline(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}

func (c *countingNotifier) NotifyAgentOnline(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
