package changes

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/db/dbtest"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
)

func TestMain(m *testing.M) {
	if err := db.InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// recorder is a Publisher that records what is published.
type recorder struct {
	mu   sync.Mutex
	sent []websocket.Message
	tops []string
}

func (r *recorder) Publish(topic string, msg websocket.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tops = append(r.tops, topic)
	r.sent = append(r.sent, msg)
}

// take returns the IDs of the "jobs" changes published since the last call,
// failing on anything else.
func (r *recorder) take(t *testing.T) []string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []string
	for i, msg := range r.sent {
		change, ok := msg.Payload.(websocket.Change)
		if r.tops[i] != websocket.ChangesTopic || msg.Type != websocket.MsgChange || !ok || change.Type != websocket.ChangeTypeJobs {
			t.Fatalf("published %q on %q, want a jobs change on %q", msg.Type, r.tops[i], websocket.ChangesTopic)
		}
		ids = append(ids, change.ID)
	}
	r.sent, r.tops = nil, nil
	return ids
}

type fixture struct {
	jobs    repositories.JobRepository
	rec     *recorder
	agentID uuid.UUID
	destID  uuid.UUID
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	gormDB := dbtest.New(t)
	agent := &db.Agent{Name: "agent", Hostname: "host", Status: "online"}
	if err := repositories.NewAgentRepository(gormDB).Create(ctx, agent); err != nil {
		t.Fatal(err)
	}
	dest := &db.Destination{Name: "dest", Type: "local"}
	if err := gormDB.WithContext(ctx).Create(dest).Error; err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	return fixture{
		jobs:    NewJobRepository(repositories.NewJobRepository(gormDB), rec),
		rec:     rec,
		agentID: agent.ID,
		destID:  dest.ID,
	}
}

func (f fixture) createJob(t *testing.T, status string) *db.Job {
	t.Helper()
	job := &db.Job{AgentID: f.agentID, Status: status, Type: "backup"}
	if err := f.jobs.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestJobRepository_PublishesEachWrite(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	now := time.Now().UTC()

	job := f.createJob(t, "pending")
	id := job.ID.String()
	if got := f.rec.take(t); !slices.Equal(got, []string{id}) {
		t.Fatalf("Create published %q, want the new job's ID", got)
	}

	steps := []struct {
		name  string
		write func() error
	}{
		{"CreateDestination", func() error {
			return f.jobs.CreateDestination(ctx, &db.JobDestination{JobID: job.ID, DestinationID: f.destID, Status: "pending"})
		}},
		{"UpdateStatus", func() error { return f.jobs.UpdateStatus(ctx, job.ID, "running", &now, nil, "") }},
		{"UpdateDestinationStatus", func() error {
			return f.jobs.UpdateDestinationStatus(ctx, job.ID, f.destID, "succeeded", &now, &now, "snap", 10, "")
		}},
		{"UpsertDestinationCommandResult", func() error {
			return f.jobs.UpsertDestinationCommandResult(ctx, job.ID, f.destID, "pgdump", "succeeded", &now, &now, "snap2", 5, "")
		}},
		{"CreateRetentionTag", func() error {
			return f.jobs.CreateRetentionTag(ctx, &db.JobRetentionTag{JobID: job.ID, DestinationID: f.destID, Tag: "policy:x", Status: "pending"})
		}},
		{"UpdateRetentionTagStatus", func() error {
			return f.jobs.UpdateRetentionTagStatus(ctx, job.ID, f.destID, "policy:x", "succeeded", &now, &now, "")
		}},
		{"Update", func() error { job.Error = "note"; return f.jobs.Update(ctx, job) }},
	}
	for _, step := range steps {
		if err := step.write(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if got := f.rec.take(t); !slices.Equal(got, []string{id}) {
			t.Errorf("%s published %q, want [%s]", step.name, got, id)
		}
	}

	// MarkResumeExhausted only applies to an interrupted job.
	interrupted := f.createJob(t, "interrupted")
	f.rec.take(t)
	if err := f.jobs.MarkResumeExhausted(ctx, interrupted.ID, "gave up"); err != nil {
		t.Fatalf("MarkResumeExhausted: %v", err)
	}
	if got := f.rec.take(t); !slices.Equal(got, []string{interrupted.ID.String()}) {
		t.Errorf("MarkResumeExhausted published %q, want [%s]", got, interrupted.ID)
	}
}

// TestJobRepository_NothingWithoutAWrite: reads, log writes and failed
// writes publish nothing.
func TestJobRepository_NothingWithoutAWrite(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	now := time.Now().UTC()
	job := f.createJob(t, "succeeded")
	f.rec.take(t)

	if _, err := f.jobs.GetByID(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.jobs.List(ctx, repositories.ListOptions{Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if err := f.jobs.BulkCreateLogs(ctx, []db.JobLog{{JobID: job.ID, Level: "info", Message: "x", Timestamp: now}}); err != nil {
		t.Fatal(err)
	}
	// A finished job refuses a late status: the write fails.
	if err := f.jobs.UpdateStatus(ctx, job.ID, "running", &now, nil, ""); !errors.Is(err, repositories.ErrTerminalState) {
		t.Fatalf("UpdateStatus on a finished job = %v, want ErrTerminalState", err)
	}
	if err := f.jobs.UpdateDestinationStatus(ctx, job.ID, uuid.New(), "succeeded", &now, &now, "", 0, ""); err == nil {
		t.Fatal("UpdateDestinationStatus of an unknown destination succeeded")
	}
	if got := f.rec.take(t); len(got) != 0 {
		t.Errorf("published %q, want nothing", got)
	}
}

// TestJobRepository_BulkWrites publish one change without ID, and only when
// rows changed.
func TestJobRepository_BulkWrites(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	if n, err := f.jobs.MarkRunningJobsInterrupted(ctx, "restart"); err != nil || n != 0 {
		t.Fatalf("MarkRunningJobsInterrupted = %d, %v", n, err)
	}
	if n, err := f.jobs.MarkRunningJobsInterruptedForAgent(ctx, f.agentID, "gone"); err != nil || n != 0 {
		t.Fatalf("MarkRunningJobsInterruptedForAgent = %d, %v", n, err)
	}
	if got := f.rec.take(t); len(got) != 0 {
		t.Fatalf("bulk writes that changed nothing published %q", got)
	}

	f.createJob(t, "running")
	f.createJob(t, "running")
	f.rec.take(t)
	if n, err := f.jobs.MarkRunningJobsInterruptedForAgent(ctx, f.agentID, "gone"); err != nil || n != 2 {
		t.Fatalf("MarkRunningJobsInterruptedForAgent = %d, %v; want 2", n, err)
	}
	if got := f.rec.take(t); !slices.Equal(got, []string{""}) {
		t.Errorf("bulk write published %q, want one change without ID", got)
	}

	f.createJob(t, "running")
	f.rec.take(t)
	if n, err := f.jobs.MarkRunningJobsInterrupted(ctx, "restart"); err != nil || n != 1 {
		t.Fatalf("MarkRunningJobsInterrupted = %d, %v; want 1", n, err)
	}
	if got := f.rec.take(t); !slices.Equal(got, []string{""}) {
		t.Errorf("bulk write published %q, want one change without ID", got)
	}
}
