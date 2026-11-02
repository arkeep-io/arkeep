package scheduler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/destqueue"
	"github.com/arkeep-io/arkeep/server/internal/notification"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	proto "github.com/arkeep-io/arkeep/shared/proto"
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
	q := destqueue.New(jobs, dests, repositories.NewSettingsRepository(gdb), s.agentMgr, zap.NewNop())
	q.RegisterStarter("backup", s)
	s.SetQueue(q)
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

	agent := &db.Agent{Name: "test-agent", Status: "online"}
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

// addRestoreJob creates a restore job on the fixture's policy, created offset
// from the interrupted job. Restores carry the PolicyID of the snapshot they
// restore, so they share the policy with the backups.
func addRestoreJob(t *testing.T, gdb *gorm.DB, f *resumeFixture, status string, offset time.Duration) {
	t.Helper()
	restore := &db.Job{
		PolicyID: &f.policy.ID,
		AgentID:  f.agentID,
		Type:     "restore",
		Status:   status,
	}
	if err := gdb.Create(restore).Error; err != nil {
		t.Fatalf("create restore job: %v", err)
	}
	if err := gdb.Model(restore).Update("created_at", f.job.CreatedAt.Add(offset)).Error; err != nil {
		t.Fatalf("set restore created_at: %v", err)
	}
}

// TestResumeInterrupted_IgnoresRestores guards against restores of the policy's
// snapshots blocking the resume of an interrupted backup: a pending restore is
// not a pending backup, and a later restore does not supersede the backup.
func TestResumeInterrupted_IgnoresRestores(t *testing.T) {
	tests := []struct {
		name   string
		status string
		offset time.Duration
	}{
		{name: "an older restore is pending", status: "pending", offset: -time.Hour},
		{name: "a newer restore exists", status: "succeeded", offset: time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, gdb, policies, jobs := newTestScheduler(t)
			f := newResumeFixture(t, gdb, policies, jobs)
			addRestoreJob(t, gdb, f, tt.status, tt.offset)

			s.ResumeInterrupted(context.Background(), f.agentID)

			resumed := 0
			for _, j := range jobsForPolicy(t, gdb, f.policy.ID) {
				if j.ResumeOfJobID != nil && *j.ResumeOfJobID == f.job.ID {
					resumed++
				}
			}
			if resumed != 1 {
				t.Errorf("found %d resume jobs, want 1", resumed)
			}
		})
	}
}

// TestTriggerNow_PendingRestoreDoesNotBlockBackup guards against a restore
// waiting for its agent suppressing the policy's backups.
func TestTriggerNow_PendingRestoreDoesNotBlockBackup(t *testing.T) {
	s, gdb, policies, jobs := newTestScheduler(t)
	f := newResumeFixture(t, gdb, policies, jobs)
	addRestoreJob(t, gdb, f, "pending", time.Minute)

	job, err := s.TriggerNow(context.Background(), f.policy.ID)
	if err != nil {
		t.Fatalf("TriggerNow: %v", err)
	}
	if job == nil {
		t.Fatal("TriggerNow returned no job: the pending restore suppressed the backup")
	}
	if job.Type != "backup" {
		t.Errorf("job Type = %q, want \"backup\"", job.Type)
	}
}

// TestTriggerNow_PendingBackupStillDeduplicates keeps the original guard: at
// most one not-yet-started backup per policy.
func TestTriggerNow_PendingBackupStillDeduplicates(t *testing.T) {
	s, gdb, policies, jobs := newTestScheduler(t)
	f := newResumeFixture(t, gdb, policies, jobs)
	if err := gdb.Model(f.job).Update("status", "pending").Error; err != nil {
		t.Fatalf("mark job pending: %v", err)
	}

	job, err := s.TriggerNow(context.Background(), f.policy.ID)
	if !errors.Is(err, ErrJobAlreadyQueued) {
		t.Errorf("TriggerNow error = %v, want ErrJobAlreadyQueued", err)
	}
	if job != nil {
		t.Errorf("TriggerNow created job %s, want none: a backup is already pending", job.ID)
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

func (c *countingNotifier) NotifyJobSucceeded(_ context.Context, _ notification.JobSubject) error {
	return nil
}

func (c *countingNotifier) NotifyJobFailed(_ context.Context, _ notification.JobSubject, _ string) error {
	c.jobFailed++
	return nil
}

func (c *countingNotifier) NotifyAgentOffline(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}

func (c *countingNotifier) NotifyAgentOnline(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}

// queueFixture is a dispatchable policy on a connected agent, plus helpers to
// create destinations, busy holders and backup jobs for the queue scenarios.
type queueFixture struct {
	t      *testing.T
	s      *Scheduler
	gdb    *gorm.DB
	jobs   repositories.JobRepository
	f      *resumeFixture
	stream *recordingStream
}

func newQueueFixture(t *testing.T) *queueFixture {
	t.Helper()
	s, gdb, policies, jobs := newTestScheduler(t)
	f := newResumeFixture(t, gdb, policies, jobs)
	// The queue rebuilds jobs from the stored policy: the shared fixture's
	// legacy sources format does not parse.
	if err := gdb.Model(f.policy).Update("sources", `[{"type":"directory","path":"/data"}]`).Error; err != nil {
		t.Fatalf("set policy sources: %v", err)
	}
	stream := &recordingStream{}
	s.agentMgr.Register(f.agentID.String(), "test-host", false, stream)
	return &queueFixture{t: t, s: s, gdb: gdb, jobs: jobs, f: f, stream: stream}
}

func (q *queueFixture) dest(name string) *db.Destination {
	q.t.Helper()
	d := &db.Destination{Name: name, Type: "local", Config: `{"path":"/backups"}`, Enabled: true}
	if err := q.s.dests.Create(context.Background(), d); err != nil {
		q.t.Fatalf("create destination: %v", err)
	}
	return d
}

// holder creates a running job holding the busy gate of d.
func (q *queueFixture) holder(d *db.Destination) *db.Job {
	q.t.Helper()
	ctx := context.Background()
	h := &db.Job{AgentID: q.f.agentID, Type: "retention", Status: "running"}
	if err := q.jobs.Create(ctx, h); err != nil {
		q.t.Fatalf("create holder job: %v", err)
	}
	if acquired, err := q.s.dests.TryAcquireBusyAll(ctx, []uuid.UUID{d.ID}, h.ID, db.LockExclusive); err != nil || !acquired {
		q.t.Fatalf("TryAcquireBusy: acquired=%v err=%v", acquired, err)
	}
	return h
}

// dispatch creates a policy over dests and one backup job for it, and
// dispatches the job like a fresh run.
func (q *queueFixture) dispatch(dests ...*db.Destination) *db.Job {
	q.t.Helper()
	return q.dispatchAs(q.f.agentID, dests...)
}

// dispatchAs is dispatch for a policy of agentID.
func (q *queueFixture) dispatchAs(agentID uuid.UUID, dests ...*db.Destination) *db.Job {
	q.t.Helper()
	ctx := context.Background()
	policy := &db.Policy{
		Name:         "policy-" + uuid.Must(uuid.NewV7()).String(),
		AgentID:      agentID,
		Schedule:     "0 2 * * *",
		Enabled:      true,
		Sources:      `[{"type":"directory","path":"/data"}]`,
		RepoPassword: db.EncryptedString("repo-secret"),
	}
	if err := q.s.policies.Create(ctx, policy); err != nil {
		q.t.Fatalf("create policy: %v", err)
	}
	job := &db.Job{PolicyID: &policy.ID, AgentID: agentID, Type: "backup", Status: "pending"}
	if err := q.jobs.Create(ctx, job); err != nil {
		q.t.Fatalf("create job: %v", err)
	}
	policyDests := make([]repositories.PolicyDestinationWithName, 0, len(dests))
	for _, d := range dests {
		if err := q.jobs.CreateDestination(ctx, &db.JobDestination{JobID: job.ID, DestinationID: d.ID, Status: "pending"}); err != nil {
			q.t.Fatalf("create job destination: %v", err)
		}
		if err := q.s.policies.AddDestination(ctx, &db.PolicyDestination{PolicyID: policy.ID, DestinationID: d.ID}); err != nil {
			q.t.Fatalf("attach destination: %v", err)
		}
		policyDests = append(policyDests, repositories.PolicyDestinationWithName{PolicyDestination: db.PolicyDestination{DestinationID: d.ID}})
	}
	if err := q.s.dispatch(job, policy, policyDests); err != nil {
		q.t.Fatalf("dispatch: %v", err)
	}
	return job
}

// agent creates a second agent with a live stream.
func (q *queueFixture) agent(name string) uuid.UUID {
	q.t.Helper()
	a := &db.Agent{Name: name, Status: "online"}
	if err := repositories.NewAgentRepository(q.gdb).Create(context.Background(), a); err != nil {
		q.t.Fatalf("create agent: %v", err)
	}
	q.s.agentMgr.Register(a.ID.String(), name, false, &recordingStream{})
	return a.ID
}

func (q *queueFixture) status(job *db.Job) string {
	q.t.Helper()
	stored, err := q.jobs.GetByID(context.Background(), job.ID)
	if err != nil {
		q.t.Fatalf("GetByID: %v", err)
	}
	return stored.Status
}

// holders returns the IDs of the jobs holding d, oldest first.
func (q *queueFixture) holders(d *db.Destination) []uuid.UUID {
	q.t.Helper()
	locks, err := q.s.dests.ListLocks(context.Background(), []uuid.UUID{d.ID})
	if err != nil {
		q.t.Fatalf("ListLocks: %v", err)
	}
	ids := make([]uuid.UUID, len(locks))
	for i, l := range locks {
		ids[i] = l.JobID
	}
	return ids
}

func (q *queueFixture) finish(job *db.Job) {
	q.t.Helper()
	if err := q.jobs.UpdateStatus(context.Background(), job.ID, "succeeded", nil, nil, ""); err != nil {
		q.t.Fatalf("finish job: %v", err)
	}
}

// TestDispatch_DestinationBusy_Queues is the core of issue #285: a backup that
// finds its destination busy waits instead of being skipped or failed, and is
// sent once the destination is released.
func TestDispatch_DestinationBusy_Queues(t *testing.T) {
	q := newQueueFixture(t)
	notif := &countingNotifier{}
	q.s.SetNotificationService(notif)
	dest := q.dest("busy-dest")
	holder := q.holder(dest)

	job := q.dispatch(dest)

	if got := q.status(job); got != "waiting" {
		t.Fatalf("job status = %q, want \"waiting\"", got)
	}
	if len(q.stream.sent()) != 0 {
		t.Errorf("agent was sent %v while the destination was busy, want nothing", q.stream.sent())
	}
	if notif.jobFailed != 0 {
		t.Errorf("NotifyJobFailed called %d times, want 0: waiting is not a failure", notif.jobFailed)
	}
	if h := q.holders(dest); len(h) != 1 || h[0] != holder.ID {
		t.Errorf("holders = %v, want only the holder %s", h, holder.ID)
	}

	q.finish(holder)
	q.s.queue.Drain(context.Background())

	if got := q.status(job); got != "pending" {
		t.Errorf("job status after drain = %q, want \"pending\" (sent to the agent)", got)
	}
	if sent := q.stream.sent(); len(sent) != 1 || sent[0] != proto.JobType_JOB_TYPE_BACKUP {
		t.Errorf("agent was sent %v, want exactly one backup", sent)
	}
	if h := q.holders(dest); len(h) != 1 || h[0] != job.ID {
		t.Errorf("holders = %v, want only the started job %s", h, job.ID)
	}
}

// TestDispatch_PartiallyBusy_ClaimsNothing: a job waits for all its
// destinations at once and holds none of them meanwhile, so it can never
// deadlock with a job holding the rest.
func TestDispatch_PartiallyBusy_ClaimsNothing(t *testing.T) {
	q := newQueueFixture(t)
	busy := q.dest("busy")
	free := q.dest("free")
	q.holder(busy)

	job := q.dispatch(busy, free)

	if got := q.status(job); got != "waiting" {
		t.Fatalf("job status = %q, want \"waiting\"", got)
	}
	if h := q.holders(free); len(h) != 0 {
		t.Errorf("free destination holders = %v, want none: a waiting job must hold nothing", h)
	}
}

// TestDispatch_BackupNextToWaitingBackup: backups hold their destinations
// shared (issue #319), so a backup waiting for a destination another one
// holds exclusively does not hold up a younger backup on a destination they
// share — the queue stays FIFO only between conflicting modes.
func TestDispatch_BackupNextToWaitingBackup(t *testing.T) {
	q := newQueueFixture(t)
	a := q.dest("a")
	b := q.dest("b")
	holderA := q.holder(a)

	older := q.dispatch(a, b) // waits for a
	younger := q.dispatch(b)  // b is free and older only wants it shared

	if got := q.status(older); got != "waiting" {
		t.Fatalf("older job status = %q, want \"waiting\" for a", got)
	}
	if got := q.status(younger); got != "pending" {
		t.Fatalf("younger job status = %q, want \"pending\" (started next to the waiting backup)", got)
	}

	q.finish(holderA)
	q.s.queue.Drain(context.Background())
	if got := q.status(older); got != "pending" {
		t.Errorf("older job status = %q, want \"pending\" once a is free, while younger still holds b", got)
	}
	if h := q.holders(b); len(h) != 2 {
		t.Errorf("b holders = %v, want both backups", h)
	}
}

// TestDispatch_QueueTimeout: a job that waits longer than the configured
// timeout is failed and reported.
func TestDispatch_QueueTimeout(t *testing.T) {
	q := newQueueFixture(t)
	dest := q.dest("busy")
	q.holder(dest)
	job := q.dispatch(dest)

	ctx := context.Background()
	if err := repositories.NewSettingsRepository(q.gdb).Set(ctx, destqueue.KeyTimeoutMinutes, db.EncryptedString("1")); err != nil {
		t.Fatalf("set timeout: %v", err)
	}
	q.s.queue.Drain(ctx)
	if got := q.status(job); got != "waiting" {
		t.Fatalf("job status = %q, want \"waiting\" before the timeout", got)
	}

	// The wait is measured from when the job entered the queue.
	if err := q.gdb.Model(&db.Job{}).Where("id = ?", job.ID).UpdateColumn("updated_at", time.Now().UTC().Add(-2*time.Minute)).Error; err != nil {
		t.Fatalf("backdate job: %v", err)
	}
	q.s.queue.Drain(ctx)
	if got := q.status(job); got != "failed" {
		t.Fatalf("job status = %q, want \"failed\" after the timeout", got)
	}
	stored, _ := q.jobs.GetByID(ctx, job.ID)
	if stored.Error != destqueue.TimeoutError {
		t.Errorf("job error = %q, want %q", stored.Error, destqueue.TimeoutError)
	}
}

// TestDispatch_AllDestinationsUnavailable is the regression case for issue
// #283: a backup none of whose destinations can be loaded must not be sent to
// the agent (it would run its hooks, back up to nothing and report success),
// but closed as failed so the user learns it did not happen.
func TestDispatch_AllDestinationsUnavailable(t *testing.T) {
	q := newQueueFixture(t)
	notif := &countingNotifier{}
	q.s.SetNotificationService(notif)

	job := &db.Job{PolicyID: &q.f.policy.ID, AgentID: q.f.agentID, Type: "backup", Status: "pending"}
	if err := q.jobs.Create(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	policyDests := []repositories.PolicyDestinationWithName{{PolicyDestination: db.PolicyDestination{DestinationID: uuid.Must(uuid.NewV7())}}}
	if err := q.s.dispatch(job, q.f.policy, policyDests); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if got := q.status(job); got != "failed" {
		t.Errorf("job status = %q, want \"failed\"", got)
	}
	if notif.jobFailed != 1 {
		t.Errorf("NotifyJobFailed called %d times, want 1", notif.jobFailed)
	}
}

// TestTriggerNow_AlreadyQueued: a manual run while the policy already has a
// queued backup is refused with ErrJobAlreadyQueued instead of returning no
// job (which the API handler would dereference).
func TestTriggerNow_AlreadyQueued(t *testing.T) {
	q := newQueueFixture(t)
	dest := q.dest("busy")
	q.holder(dest)
	ctx := context.Background()
	if err := q.s.policies.AddDestination(ctx, &db.PolicyDestination{PolicyID: q.f.policy.ID, DestinationID: dest.ID}); err != nil {
		t.Fatalf("attach destination: %v", err)
	}

	job, err := q.s.TriggerNow(ctx, q.f.policy.ID)
	if err != nil {
		t.Fatalf("first TriggerNow: %v", err)
	}
	if got := q.status(job); got != "waiting" {
		t.Fatalf("first job status = %q, want \"waiting\"", got)
	}

	if _, err := q.s.TriggerNow(ctx, q.f.policy.ID); !errors.Is(err, ErrJobAlreadyQueued) {
		t.Errorf("second TriggerNow error = %v, want ErrJobAlreadyQueued", err)
	}
}

// TestDispatch_AgentGoneBeforeRunning_FreesDestination is the regression case
// for issue #319: a backup sent to an agent that vanishes before reporting
// "running" stays pending, and must not keep its destination busy — a backup of
// another agent on the same destination would otherwise wait until the gone
// agent came back or the job was cancelled by hand.
func TestDispatch_AgentGoneBeforeRunning_FreesDestination(t *testing.T) {
	q := newQueueFixture(t)
	ctx := context.Background()
	dest := q.dest("shared")

	stranded := q.dispatch(dest)
	if got := q.status(stranded); got != "pending" {
		t.Fatalf("job status = %q, want \"pending\" (sent to the agent)", got)
	}

	// The agent drops: the disconnect cleanup of the gRPC server and watchdog.
	q.s.agentMgr.DeregisterStale(q.f.agentID.String(), time.Now().Add(time.Second))
	if _, err := q.jobs.MarkRunningJobsInterruptedForAgent(ctx, q.f.agentID, "agent disconnected"); err != nil {
		t.Fatalf("MarkRunningJobsInterruptedForAgent: %v", err)
	}
	if got := q.status(stranded); got != "pending" {
		t.Errorf("stranded job status = %q, want \"pending\" for DispatchPending", got)
	}

	other := q.dispatchAs(q.agent("other-agent"), dest)
	if got := q.status(other); got != "pending" {
		t.Errorf("other agent's job status = %q, want \"pending\" (sent, not queued behind the gone agent)", got)
  }
}

// TestScheduledRunAdvancesNextRunAt covers issue #321: after a scheduled run the
// policy's next_run_at must point at the next cron firing, not be left equal to
// last_run_at (which made the UI show NEXT RUN == LAST RUN for hourly schedules).
func TestScheduledRunAdvancesNextRunAt(t *testing.T) {
	s, gdb, policies, _ := newTestScheduler(t)
	ctx := context.Background()

	agent := &db.Agent{Name: "test-agent", Status: "online"}
	if err := repositories.NewAgentRepository(gdb).Create(ctx, agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	policy := &db.Policy{
		Name:              "hourly-policy",
		AgentID:           agent.ID,
		Schedule:          "0 * * * *", // hourly, on the top of the hour
		Enabled:           true,
		Sources:           `["/data"]`,
		RepoPassword:      db.EncryptedString("repo-secret"),
		ResumeInterrupted: true,
	}
	if err := policies.Create(ctx, policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	if _, err := s.createAndDispatch(policy, nil, nil); err != nil {
		t.Fatalf("createAndDispatch: %v", err)
	}

	stored, err := policies.GetByID(ctx, policy.ID)
	if err != nil {
		t.Fatalf("GetByID(policy): %v", err)
	}
	if stored.LastRunAt == nil {
		t.Fatal("policy LastRunAt = nil, want it set after a scheduled run")
	}
	if stored.NextRunAt == nil {
		t.Fatal("policy NextRunAt = nil, want it set after a scheduled run")
	}
	if !stored.NextRunAt.After(*stored.LastRunAt) {
		t.Errorf("policy NextRunAt (%v) must be after LastRunAt (%v) for an hourly schedule",
			*stored.NextRunAt, *stored.LastRunAt)
	}
	if d := stored.NextRunAt.Sub(*stored.LastRunAt); d <= 0 || d > time.Hour {
		t.Errorf("NextRunAt-LastRunAt = %v, want in (0, 1h] for an hourly schedule", d)
	}
}

// TestNextScheduledRunAt_UsesSchedulerLocation: gocron fires a schedule without
// an explicit CRON_TZ in the scheduler's location (time.Local), so next_run_at
// must be computed there too — computed in UTC, a "0 2 * * *" policy on a
// server with TZ=Europe/Rome showed 02:00 UTC instead of 02:00 local time.
func TestNextScheduledRunAt_UsesSchedulerLocation(t *testing.T) {
	load := func(name string) *time.Location {
		t.Helper()
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatalf("LoadLocation(%q): %v", name, err)
		}
		return loc
	}
	now := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		local    *time.Location
		schedule string
		want     time.Time
	}{
		{"daily in Europe/Rome", load("Europe/Rome"), "0 2 * * *", time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)},
		{"hourly with a half-hour offset", load("Asia/Kolkata"), "0 * * * *", time.Date(2026, 10, 12, 10, 30, 0, 0, time.UTC)},
		{"explicit CRON_TZ wins", load("Europe/Rome"), "CRON_TZ=UTC 0 2 * * *", time.Date(2026, 10, 13, 2, 0, 0, 0, time.UTC)},
		{"descriptor", time.UTC, "@hourly", time.Date(2026, 10, 12, 11, 0, 0, 0, time.UTC)},
		{"unparseable falls back to now", time.UTC, "not a schedule", now},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nextScheduledRunAt(tc.schedule, now, tc.local)
			if !got.Equal(tc.want) {
				t.Errorf("nextScheduledRunAt(%q) = %v, want %v", tc.schedule, got, tc.want)
			}
			if got.Location() != time.UTC {
				t.Errorf("nextScheduledRunAt location = %v, want UTC", got.Location())
			}
		})
	}
}

// newSchedulePolicy creates an enabled policy with schedule on a new agent.
func newSchedulePolicy(t *testing.T, gdb *gorm.DB, policies repositories.PolicyRepository, schedule string) *db.Policy {
	t.Helper()
	ctx := context.Background()
	agent := &db.Agent{Name: "agent-" + uuid.Must(uuid.NewV7()).String(), Status: "online"}
	if err := repositories.NewAgentRepository(gdb).Create(ctx, agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	policy := &db.Policy{
		Name:         "policy-" + uuid.Must(uuid.NewV7()).String(),
		AgentID:      agent.ID,
		Schedule:     schedule,
		Enabled:      true,
		Sources:      `[{"type":"directory","path":"/data"}]`,
		RepoPassword: db.EncryptedString("repo-secret"),
	}
	if err := policies.Create(ctx, policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	return policy
}

// storedNextRunAt reads a policy's next_run_at back from the database.
func storedNextRunAt(t *testing.T, policies repositories.PolicyRepository, id uuid.UUID) *time.Time {
	t.Helper()
	stored, err := policies.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	return stored.NextRunAt
}

// TestAddPolicy_SetsNextRunAt: a new or re-enabled policy shows its next run
// right away, not only after its first run (issue #321).
func TestAddPolicy_SetsNextRunAt(t *testing.T) {
	s, gdb, policies, _ := newTestScheduler(t)
	policy := newSchedulePolicy(t, gdb, policies, "0 * * * *")

	if err := s.AddPolicy(policy); err != nil {
		t.Fatalf("AddPolicy: %v", err)
	}

	stored := storedNextRunAt(t, policies, policy.ID)
	if stored == nil {
		t.Fatal("stored NextRunAt = nil, want the next firing")
	}
	if d := time.Until(*stored); d <= 0 || d > time.Hour {
		t.Errorf("stored NextRunAt in %v, want within the next hour", d)
	}
	if policy.NextRunAt == nil || !policy.NextRunAt.Equal(*stored) {
		t.Errorf("policy.NextRunAt = %v, want the stored %v (the API responds with this struct)", policy.NextRunAt, *stored)
	}
}

// TestUpdatePolicy_ScheduleChangeRefreshesNextRunAt: changing the schedule
// moves next_run_at to the new schedule's next firing.
func TestUpdatePolicy_ScheduleChangeRefreshesNextRunAt(t *testing.T) {
	s, gdb, policies, _ := newTestScheduler(t)

	// A daily slot always more than an hour away, in the scheduler's location.
	daily := fmt.Sprintf("0 %d * * *", (time.Now().In(s.location).Hour()+12)%24)
	policy := newSchedulePolicy(t, gdb, policies, daily)
	if err := s.AddPolicy(policy); err != nil {
		t.Fatalf("AddPolicy: %v", err)
	}

	policy.Schedule = "0 * * * *"
	if err := s.UpdatePolicy(policy); err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}

	stored := storedNextRunAt(t, policies, policy.ID)
	if stored == nil {
		t.Fatal("stored NextRunAt = nil after a schedule change")
	}
	if d := time.Until(*stored); d <= 0 || d > time.Hour {
		t.Errorf("stored NextRunAt in %v after switching to hourly, want within the next hour", d)
	}
}

// TestRunJob_SkippedTickAdvancesNextRunAt: a tick skipped because a backup of
// the policy is still queued must still move next_run_at to the next firing,
// instead of leaving it in the past — without recording a run.
func TestRunJob_SkippedTickAdvancesNextRunAt(t *testing.T) {
	s, gdb, policies, jobs := newTestScheduler(t)
	ctx := context.Background()
	policy := newSchedulePolicy(t, gdb, policies, "0 * * * *")

	past := time.Now().UTC().Add(-2 * time.Hour)
	if err := policies.UpdateSchedule(ctx, policy.ID, past, past); err != nil {
		t.Fatalf("UpdateSchedule: %v", err)
	}
	queued := &db.Job{PolicyID: &policy.ID, AgentID: policy.AgentID, Type: "backup", Status: "pending"}
	if err := jobs.Create(ctx, queued); err != nil {
		t.Fatalf("create pending job: %v", err)
	}

	job, err := s.runJob(policy, nil)
	if err != nil || job != nil {
		t.Fatalf("runJob = %v, %v; want a skipped tick (nil, nil)", job, err)
	}

	stored, err := policies.GetByID(ctx, policy.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.NextRunAt == nil || !stored.NextRunAt.After(time.Now()) {
		t.Errorf("NextRunAt = %v, want a future firing after a skipped tick", stored.NextRunAt)
	}
	if stored.LastRunAt == nil || !stored.LastRunAt.Equal(past) {
		t.Errorf("LastRunAt = %v, want it left at %v: a skipped tick is not a run", stored.LastRunAt, past)
	}
}

// TestRunJob_AfterPendingTimeout is the scenario of issue #325: a daily backup
// whose job stays pending (agent offline) used to skip every later run in
// silence. Once the queue timeout fails it, the next tick creates a new job.
func TestRunJob_AfterPendingTimeout(t *testing.T) {
	q := newQueueFixture(t)
	ctx := context.Background()

	stuck := &db.Job{PolicyID: &q.f.policy.ID, AgentID: q.f.agentID, Type: "backup", Status: "pending"}
	if err := q.jobs.Create(ctx, stuck); err != nil {
		t.Fatalf("create pending job: %v", err)
	}
	if job, err := q.s.runJob(q.f.policy, nil); err != nil || job != nil {
		t.Fatalf("runJob with a pending job = %v, %v; want the tick skipped", job, err)
	}

	if err := q.gdb.Model(&db.Job{}).Where("id = ?", stuck.ID).UpdateColumn("updated_at", time.Now().UTC().Add(-25*time.Hour)).Error; err != nil {
		t.Fatalf("backdate job: %v", err)
	}
	q.s.queue.Drain(ctx)
	if got := q.status(stuck); got != "failed" {
		t.Fatalf("stuck job status = %q, want \"failed\" after the timeout", got)
	}

	job, err := q.s.runJob(q.f.policy, nil)
	if err != nil || job == nil {
		t.Fatalf("runJob after the timeout = %v, %v; want a new job", job, err)
	}
}

// pause disables d. Destination.Enabled carries default:true, so a destination
// created with Enabled false would be stored enabled: flip it with an update.
func (q *queueFixture) pause(d *db.Destination) {
	q.t.Helper()
	if err := q.gdb.Model(d).UpdateColumn("enabled", false).Error; err != nil {
		q.t.Fatalf("disable destination: %v", err)
	}
}

// jobDestinations returns the destination ID and status of every
// JobDestination of job.
func (q *queueFixture) jobDestinations(job *db.Job) map[uuid.UUID]repositories.JobDestinationWithName {
	q.t.Helper()
	rows, err := q.jobs.ListDestinationsByJob(context.Background(), job.ID)
	if err != nil {
		q.t.Fatalf("ListDestinationsByJob: %v", err)
	}
	out := make(map[uuid.UUID]repositories.JobDestinationWithName, len(rows))
	for _, r := range rows {
		out[r.DestinationID] = r
	}
	return out
}

// TestTriggerNow_SkipsDisabledDestination: a disabled destination of the policy
// takes no part in the backup and does not appear in the job.
func TestTriggerNow_SkipsDisabledDestination(t *testing.T) {
	q := newQueueFixture(t)
	ctx := context.Background()
	active := q.dest("active")
	paused := q.dest("paused")
	q.pause(paused)
	for _, d := range []*db.Destination{active, paused} {
		if err := q.s.policies.AddDestination(ctx, &db.PolicyDestination{PolicyID: q.f.policy.ID, DestinationID: d.ID}); err != nil {
			t.Fatalf("attach destination: %v", err)
		}
	}

	job, err := q.s.TriggerNow(ctx, q.f.policy.ID)
	if err != nil {
		t.Fatalf("TriggerNow: %v", err)
	}

	if got := q.status(job); got != "pending" {
		t.Fatalf("job status = %q, want \"pending\" (sent to the agent)", got)
	}
	jds := q.jobDestinations(job)
	if _, ok := jds[active.ID]; len(jds) != 1 || !ok {
		t.Errorf("job destinations = %v, want only the enabled one", jds)
	}
	if h := q.holders(paused); len(h) != 0 {
		t.Errorf("disabled destination holders = %v, want none", h)
	}
}

// TestTriggerNow_AllDestinationsDisabled: a policy whose destinations are all
// disabled does not back up to nothing; the job fails and is reported.
func TestTriggerNow_AllDestinationsDisabled(t *testing.T) {
	q := newQueueFixture(t)
	notif := &countingNotifier{}
	q.s.SetNotificationService(notif)
	ctx := context.Background()
	paused := q.dest("paused")
	q.pause(paused)
	if err := q.s.policies.AddDestination(ctx, &db.PolicyDestination{PolicyID: q.f.policy.ID, DestinationID: paused.ID}); err != nil {
		t.Fatalf("attach destination: %v", err)
	}

	job, err := q.s.TriggerNow(ctx, q.f.policy.ID)
	if err != nil {
		t.Fatalf("TriggerNow: %v", err)
	}

	stored, err := q.jobs.GetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.Status != "failed" || stored.Error != errAllDestinationsDisabled {
		t.Errorf("job = %q %q, want \"failed\" %q", stored.Status, stored.Error, errAllDestinationsDisabled)
	}
	if len(q.stream.sent()) != 0 {
		t.Errorf("agent was sent %v, want nothing", q.stream.sent())
	}
	if notif.jobFailed != 1 {
		t.Errorf("NotifyJobFailed called %d times, want 1", notif.jobFailed)
	}
}

// TestDispatch_DestinationDisabledWhileWaiting: a destination disabled while
// its backup waits in the queue is skipped when the backup starts.
func TestDispatch_DestinationDisabledWhileWaiting(t *testing.T) {
	q := newQueueFixture(t)
	busy := q.dest("busy")
	other := q.dest("other")
	holder := q.holder(busy)

	job := q.dispatch(busy, other)
	if got := q.status(job); got != "waiting" {
		t.Fatalf("job status = %q, want \"waiting\"", got)
	}

	q.pause(other)
	q.finish(holder)
	q.s.queue.Drain(context.Background())

	if got := q.status(job); got != "pending" {
		t.Fatalf("job status after drain = %q, want \"pending\" (sent to the agent)", got)
	}
	jds := q.jobDestinations(job)
	if got := jds[other.ID]; got.Status != "skipped" || got.Error != "destination disabled" {
		t.Errorf("disabled destination = %q %q, want \"skipped\" \"destination disabled\"", got.Status, got.Error)
	}
	if got := jds[busy.ID]; got.Status != "pending" {
		t.Errorf("enabled destination status = %q, want \"pending\"", got.Status)
	}
	if h := q.holders(other); len(h) != 0 {
		t.Errorf("disabled destination holders = %v, want none", h)
	}
}
