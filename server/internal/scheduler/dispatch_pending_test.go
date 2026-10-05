package scheduler

import (
	"context"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"

	"github.com/arkeep-io/arkeep/server/internal/db"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// recordingStream is the StreamJobs stream of an in-process agent that only
// records the job types it is sent.
type recordingStream struct {
	grpc.ServerStream
	mu    sync.Mutex
	types []proto.JobType
}

func (r *recordingStream) Send(a *proto.JobAssignment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.types = append(r.types, a.Type)
	return nil
}

func (r *recordingStream) sent() []proto.JobType {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]proto.JobType(nil), r.types...)
}

func TestDispatchPending_OnlyRebuildsBackups(t *testing.T) {
	s, gdb, policies, jobs := newTestScheduler(t)
	f := newResumeFixture(t, gdb, policies, jobs)
	ctx := context.Background()
	// The shared fixture's legacy sources format does not parse; the backup
	// payload must build for the backup to actually be dispatched.
	if err := gdb.Model(f.policy).Update("sources", `[{"type":"directory","path":"/data"}]`).Error; err != nil {
		t.Fatalf("set policy sources: %v", err)
	}

	pending := func(jobType string, withPolicy bool) *db.Job {
		t.Helper()
		j := &db.Job{AgentID: f.agentID, Type: jobType, Status: "pending"}
		if withPolicy {
			j.PolicyID = &f.policy.ID
		}
		if err := jobs.Create(ctx, j); err != nil {
			t.Fatalf("create %s job: %v", jobType, err)
		}
		return j
	}
	backup := pending("backup", true)
	policyRestore := pending("restore", true)
	importedRestore := pending("restore", false)

	stream := &recordingStream{}
	s.agentMgr.Register(f.agentID.String(), "test-host", false, stream)

	s.DispatchPending(ctx, f.agentID)

	sent := stream.sent()
	if len(sent) != 1 || sent[0] != proto.JobType_JOB_TYPE_BACKUP {
		t.Errorf("agent was sent %v, want exactly one backup (restores must never be rebuilt as backups)", sent)
	}

	for _, tc := range []struct {
		name string
		job  *db.Job
	}{
		{"restore with a policy", policyRestore},
		{"restore of an imported snapshot", importedRestore},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got db.Job
			if err := gdb.First(&got, "id = ?", tc.job.ID).Error; err != nil {
				t.Fatalf("load job: %v", err)
			}
			if got.Status != "failed" {
				t.Errorf("status = %q, want failed", got.Status)
			}
			if !strings.Contains(got.Error, "offline") {
				t.Errorf("error = %q, want it to explain the agent was offline", got.Error)
			}
			if got.EndedAt == nil {
				t.Error("ended_at is nil, want it set")
			}
		})
	}

	var gotBackup db.Job
	if err := gdb.First(&gotBackup, "id = ?", backup.ID).Error; err != nil {
		t.Fatalf("load backup job: %v", err)
	}
	if gotBackup.Status == "failed" {
		t.Errorf("backup job was failed (%q), want it dispatched", gotBackup.Error)
	}
}
