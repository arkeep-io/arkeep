package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// createDBJob inserts a job record with a real agent and policy to satisfy FK constraints.
func createDBJob(t *testing.T, deps *testDeps) *db.Job {
	t.Helper()
	return createDBJobWith(t, deps, "backup", "succeeded")
}

// createDBJobWith inserts a job with the given type and status.
func createDBJobWith(t *testing.T, deps *testDeps, jobType, status string) *db.Job {
	t.Helper()
	agent := createDBAgent(t, deps, "test-agent-"+uuid.NewString())
	policy := createDBPolicy(t, deps, "test-policy-"+uuid.NewString(), agent.ID)
	job := &db.Job{
		PolicyID: &policy.ID,
		AgentID:  agent.ID,
		Type:     jobType,
		Status:   status,
	}
	if err := deps.jobs.Create(context.Background(), job); err != nil {
		t.Fatalf("createDBJobWith: %v", err)
	}
	return job
}

func TestJobHandler_List(t *testing.T) {
	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/jobs", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("returns empty list on fresh DB", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/jobs", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		}
		decodeData(t, resp, &data)
		if data.Total != 0 {
			t.Errorf("total = %d, want 0", data.Total)
		}
	})

	t.Run("returns created jobs", func(t *testing.T) {
		e := newTestEnv(t)
		createDBJob(t, e.deps)
		createDBJob(t, e.deps)

		resp := e.get(t, "/api/v1/jobs", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		}
		decodeData(t, resp, &data)
		if data.Total != 2 {
			t.Errorf("total = %d, want 2", data.Total)
		}
	})

	t.Run("regular user can list jobs", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/jobs", e.userToken(t))
		assertStatus(t, resp, http.StatusOK)
	})

	t.Run("returns 400 for invalid policy_id filter", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/jobs?policy_id=not-a-uuid", e.adminToken(t))
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 for invalid agent_id filter", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/jobs?agent_id=not-a-uuid", e.adminToken(t))
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("filters by policy_id", func(t *testing.T) {
		e := newTestEnv(t)
		j := createDBJob(t, e.deps)
		createDBJob(t, e.deps) // different policy

		resp := e.get(t, "/api/v1/jobs?policy_id="+j.PolicyID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		}
		decodeData(t, resp, &data)
		if data.Total != 1 {
			t.Errorf("total = %d, want 1 (filtered by policy)", data.Total)
		}
	})

	t.Run("returns 400 for invalid status filter", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/jobs?status=invalid", e.adminToken(t))
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 for invalid type filter", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/jobs?type=invalid", e.adminToken(t))
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("filters by status", func(t *testing.T) {
		e := newTestEnv(t)
		createDBJobWith(t, e.deps, "backup", "succeeded")
		createDBJobWith(t, e.deps, "backup", "failed")
		createDBJobWith(t, e.deps, "backup", "failed")

		resp := e.get(t, "/api/v1/jobs?status=failed", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		}
		decodeData(t, resp, &data)
		if data.Total != 2 {
			t.Errorf("total = %d, want 2 (filtered by status=failed)", data.Total)
		}
	})

	t.Run("filters by type", func(t *testing.T) {
		e := newTestEnv(t)
		createDBJobWith(t, e.deps, "backup", "succeeded")
		createDBJobWith(t, e.deps, "backup", "succeeded")
		createDBJobWith(t, e.deps, "restore", "succeeded")

		resp := e.get(t, "/api/v1/jobs?type=restore", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		}
		decodeData(t, resp, &data)
		if data.Total != 1 {
			t.Errorf("total = %d, want 1 (filtered by type=restore)", data.Total)
		}
	})

	t.Run("filters by status and type combined", func(t *testing.T) {
		e := newTestEnv(t)
		createDBJobWith(t, e.deps, "backup", "failed")
		createDBJobWith(t, e.deps, "restore", "failed")
		createDBJobWith(t, e.deps, "backup", "succeeded")

		resp := e.get(t, "/api/v1/jobs?status=failed&type=backup", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		}
		decodeData(t, resp, &data)
		if data.Total != 1 {
			t.Errorf("total = %d, want 1 (filtered by status=failed&type=backup)", data.Total)
		}
	})
}

// TestJobHandler_List_CombinedFilters guards against an ID filter silently
// dropping the status and type filters given with it.
func TestJobHandler_List_CombinedFilters(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	failed := createDBJobWith(t, e.deps, "backup", "failed")
	// Same policy and agent as failed, different status and type.
	for _, jt := range []struct{ typ, status string }{{"backup", "succeeded"}, {"restore", "failed"}} {
		j := &db.Job{PolicyID: failed.PolicyID, AgentID: failed.AgentID, Type: jt.typ, Status: jt.status}
		if err := e.deps.jobs.Create(ctx, j); err != nil {
			t.Fatalf("create job: %v", err)
		}
	}
	dest := createDBDestination(t, e.deps, "dest-"+uuid.NewString(), "local")
	var onDest []uuid.UUID
	if err := e.deps.gdb.Model(&db.Job{}).Where("policy_id = ?", failed.PolicyID).Pluck("id", &onDest).Error; err != nil {
		t.Fatalf("load jobs: %v", err)
	}
	for _, id := range onDest {
		if err := e.deps.jobs.CreateDestination(ctx, &db.JobDestination{JobID: id, DestinationID: dest.ID, Status: "pending"}); err != nil {
			t.Fatalf("create job destination: %v", err)
		}
	}
	createDBJobWith(t, e.deps, "backup", "failed") // other policy, agent and no destination

	tests := []struct {
		query string
		want  int64
	}{
		{"policy_id=" + failed.PolicyID.String(), 3},
		{"policy_id=" + failed.PolicyID.String() + "&status=failed", 2},
		{"policy_id=" + failed.PolicyID.String() + "&status=failed&type=backup", 1},
		{"agent_id=" + failed.AgentID.String() + "&type=restore", 1},
		{"destination_id=" + dest.ID.String() + "&status=succeeded", 1},
		{"destination_id=" + dest.ID.String() + "&agent_id=" + failed.AgentID.String() + "&type=backup", 2},
		{"status=failed", 3},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			resp := e.get(t, "/api/v1/jobs?"+tt.query, e.adminToken(t))
			assertStatus(t, resp, http.StatusOK)

			var data struct {
				Items []any `json:"items"`
				Total int64 `json:"total"`
			}
			decodeData(t, resp, &data)
			if data.Total != tt.want || int64(len(data.Items)) != tt.want {
				t.Errorf("total = %d, items = %d, want %d", data.Total, len(data.Items), tt.want)
			}
		})
	}
}

func TestJobHandler_GetByID(t *testing.T) {
	t.Run("returns job by UUID", func(t *testing.T) {
		e := newTestEnv(t)
		job := createDBJob(t, e.deps)

		resp := e.get(t, "/api/v1/jobs/"+job.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			ID     string `json:"id"`
			Type   string `json:"type"`
			Status string `json:"status"`
		}
		decodeData(t, resp, &data)
		if data.ID != job.ID.String() {
			t.Errorf("id = %q, want %q", data.ID, job.ID.String())
		}
		if data.Status != "succeeded" {
			t.Errorf("status = %q, want succeeded", data.Status)
		}
	})

	t.Run("returns 404 for non-existent job", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/jobs/00000000-0000-0000-0000-000000000001", e.adminToken(t))
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("includes command_sources when the job has command-source results", func(t *testing.T) {
		e := newTestEnv(t)
		job := createDBJob(t, e.deps)
		dest := createDBDestination(t, e.deps, "my-destination", "local")

		if err := e.deps.jobs.UpsertDestinationCommandResult(
			context.Background(), job.ID, dest.ID, "pgdump", "succeeded",
			nil, nil, "snap-xyz", 12345, "",
		); err != nil {
			t.Fatalf("UpsertDestinationCommandResult: %v", err)
		}

		resp := e.get(t, "/api/v1/jobs/"+job.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			CommandSources []struct {
				DestinationID   string `json:"destination_id"`
				DestinationName string `json:"destination_name"`
				SourceName      string `json:"source_name"`
				Status          string `json:"status"`
				SnapshotID      string `json:"snapshot_id"`
				SizeBytes       int64  `json:"size_bytes"`
			} `json:"command_sources"`
		}
		decodeData(t, resp, &data)
		if len(data.CommandSources) != 1 {
			t.Fatalf("len(command_sources) = %d, want 1: %+v", len(data.CommandSources), data.CommandSources)
		}
		got := data.CommandSources[0]
		if got.SourceName != "pgdump" {
			t.Errorf("source_name = %q, want %q", got.SourceName, "pgdump")
		}
		if got.DestinationName != "my-destination" {
			t.Errorf("destination_name = %q, want %q", got.DestinationName, "my-destination")
		}
		if got.Status != "succeeded" || got.SnapshotID != "snap-xyz" || got.SizeBytes != 12345 {
			t.Errorf("unexpected command source fields: %+v", got)
		}
	})

	t.Run("omits command_sources for a job with none", func(t *testing.T) {
		e := newTestEnv(t)
		job := createDBJob(t, e.deps)

		resp := e.get(t, "/api/v1/jobs/"+job.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			CommandSources []any `json:"command_sources"`
		}
		decodeData(t, resp, &data)
		if len(data.CommandSources) != 0 {
			t.Errorf("command_sources = %v, want empty/absent", data.CommandSources)
		}
	})

	t.Run("returns 400 for malformed UUID", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/jobs/not-a-uuid", e.adminToken(t))
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/jobs/00000000-0000-0000-0000-000000000001", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestJobHandler_GetLogs(t *testing.T) {
	t.Run("returns empty logs for job with no logs", func(t *testing.T) {
		e := newTestEnv(t)
		job := createDBJob(t, e.deps)

		resp := e.get(t, "/api/v1/jobs/"+job.ID.String()+"/logs", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data []any
		decodeData(t, resp, &data)
		if len(data) != 0 {
			t.Errorf("len(logs) = %d, want 0", len(data))
		}
	})

	t.Run("returns 400 for malformed UUID", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/jobs/not-a-uuid/logs", e.adminToken(t))
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/jobs/00000000-0000-0000-0000-000000000001/logs", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestJobHandler_ListByPolicy(t *testing.T) {
	t.Run("returns jobs for a specific policy", func(t *testing.T) {
		e := newTestEnv(t)
		j := createDBJob(t, e.deps)
		// Insert a second job with the same policy ID.
		job2 := &db.Job{
			PolicyID: j.PolicyID,
			AgentID:  j.AgentID,
			Type:     "backup",
			Status:   "pending",
		}
		if err := e.deps.jobs.Create(context.Background(), job2); err != nil {
			t.Fatalf("createDBJob 2: %v", err)
		}
		createDBJob(t, e.deps) // unrelated policy

		resp := e.get(t, "/api/v1/policies/"+j.PolicyID.String()+"/jobs", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		}
		decodeData(t, resp, &data)
		if data.Total != 2 {
			t.Errorf("total = %d, want 2", data.Total)
		}
	})

	t.Run("returns 400 for malformed policy UUID", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/policies/not-a-uuid/jobs", e.adminToken(t))
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/policies/00000000-0000-0000-0000-000000000001/jobs", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}
