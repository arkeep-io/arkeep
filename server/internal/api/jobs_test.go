package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// createDBJob inserts a job record directly using random policy/agent UUIDs.
// SQLite does not enforce foreign keys by default, so the UUIDs do not need
// to reference real records.
func createDBJob(t *testing.T, deps *testDeps) *db.Job {
	t.Helper()
	job := &db.Job{
		PolicyID: uuid.New(),
		AgentID:  uuid.New(),
		Type:     "backup",
		Status:   "succeeded",
	}
	if err := deps.jobs.Create(context.Background(), job); err != nil {
		t.Fatalf("createDBJob: %v", err)
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
			AgentID:  uuid.New(),
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
