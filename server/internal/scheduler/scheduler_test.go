package scheduler

import (
	"bytes"
	"context"
	"errors"
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
	if acquired, err := q.s.dests.TryAcquireBusy(ctx, d.ID, h.ID); err != nil || !acquired {
		q.t.Fatalf("TryAcquireBusy: acquired=%v err=%v", acquired, err)
	}
	return h
}

// dispatch creates a policy over dests and one backup job for it, and
// dispatches the job like a fresh run.
func (q *queueFixture) dispatch(dests ...*db.Destination) *db.Job {
	q.t.Helper()
	ctx := context.Background()
	policy := &db.Policy{
		Name:         "policy-" + uuid.Must(uuid.NewV7()).String(),
		AgentID:      q.f.agentID,
		Schedule:     "0 2 * * *",
		Enabled:      true,
		Sources:      `[{"type":"directory","path":"/data"}]`,
		RepoPassword: db.EncryptedString("repo-secret"),
	}
	if err := q.s.policies.Create(ctx, policy); err != nil {
		q.t.Fatalf("create policy: %v", err)
	}
	job := &db.Job{PolicyID: &policy.ID, AgentID: q.f.agentID, Type: "backup", Status: "pending"}
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

func (q *queueFixture) status(job *db.Job) string {
	q.t.Helper()
	stored, err := q.jobs.GetByID(context.Background(), job.ID)
	if err != nil {
		q.t.Fatalf("GetByID: %v", err)
	}
	return stored.Status
}

func (q *queueFixture) busyHolder(d *db.Destination) *uuid.UUID {
	q.t.Helper()
	got, err := q.s.dests.GetByID(context.Background(), d.ID)
	if err != nil {
		q.t.Fatalf("GetByID: %v", err)
	}
	return got.BusyJobID
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
	if h := q.busyHolder(dest); h == nil || *h != holder.ID {
		t.Errorf("BusyJobID = %v, want the holder %s", h, holder.ID)
	}

	q.finish(holder)
	q.s.queue.Drain(context.Background())

	if got := q.status(job); got != "pending" {
		t.Errorf("job status after drain = %q, want \"pending\" (sent to the agent)", got)
	}
	if sent := q.stream.sent(); len(sent) != 1 || sent[0] != proto.JobType_JOB_TYPE_BACKUP {
		t.Errorf("agent was sent %v, want exactly one backup", sent)
	}
	if h := q.busyHolder(dest); h == nil || *h != job.ID {
		t.Errorf("BusyJobID = %v, want the started job %s", h, job.ID)
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
	if h := q.busyHolder(free); h != nil {
		t.Errorf("free destination BusyJobID = %v, want nil: a waiting job must hold nothing", h)
	}
}

// TestDispatch_StrictFIFO: a younger job must not overtake an older waiting
// job on a destination they share, even while that destination is free —
// otherwise a job needing several destinations could starve.
func TestDispatch_StrictFIFO(t *testing.T) {
	q := newQueueFixture(t)
	a := q.dest("a")
	b := q.dest("b")
	holderA := q.holder(a)

	older := q.dispatch(a, b) // waits for a
	younger := q.dispatch(b)  // b is free, but older is waiting for it

	if got := q.status(younger); got != "waiting" {
		t.Fatalf("younger job status = %q, want \"waiting\" behind the older one", got)
	}
	q.s.queue.Drain(context.Background())
	if got := q.status(younger); got != "waiting" {
		t.Fatalf("younger job status after drain = %q, want still \"waiting\"", got)
	}

	q.finish(holderA)
	q.s.queue.Drain(context.Background())
	if got := q.status(older); got != "pending" {
		t.Fatalf("older job status = %q, want \"pending\" (started first)", got)
	}
	if got := q.status(younger); got != "waiting" {
		t.Fatalf("younger job status = %q, want \"waiting\" while the older one holds b", got)
	}

	q.finish(older)
	q.s.queue.Drain(context.Background())
	if got := q.status(younger); got != "pending" {
		t.Errorf("younger job status = %q, want \"pending\" once b is free", got)
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
