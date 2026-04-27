package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// createDBPolicy inserts a policy record directly and returns it.
func createDBPolicy(t *testing.T, deps *testDeps, name string, agentID uuid.UUID) *db.Policy {
	t.Helper()
	p := &db.Policy{
		Name:             name,
		AgentID:          agentID,
		Schedule:         "@daily",
		Enabled:          true,
		Sources:          `["/data"]`,
		RepoPassword:     "secret",
		RetentionDaily:   7,
		RetentionWeekly:  4,
		RetentionMonthly: 6,
		RetentionYearly:  1,
	}
	if err := deps.policies.Create(context.Background(), p); err != nil {
		t.Fatalf("createDBPolicy: %v", err)
	}
	return p
}

func TestPolicyHandler_List(t *testing.T) {
	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/policies", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("returns empty list on fresh DB", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/policies", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		}
		decodeData(t, resp, &data)
		if len(data.Items) != 0 {
			t.Errorf("items len = %d, want 0", len(data.Items))
		}
	})

	t.Run("returns created policies", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := uuid.New()
		createDBPolicy(t, e.deps, "backup-home", agentID)
		createDBPolicy(t, e.deps, "backup-db", agentID)

		resp := e.get(t, "/api/v1/policies", e.adminToken(t))
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
}

func TestPolicyHandler_GetByID(t *testing.T) {
	t.Run("returns policy by UUID", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := uuid.New()
		policy := createDBPolicy(t, e.deps, "my-policy", agentID)

		resp := e.get(t, "/api/v1/policies/"+policy.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Schedule string `json:"schedule"`
		}
		decodeData(t, resp, &data)
		if data.ID != policy.ID.String() {
			t.Errorf("id = %q, want %q", data.ID, policy.ID.String())
		}
		if data.Name != "my-policy" {
			t.Errorf("name = %q, want my-policy", data.Name)
		}
	})

	t.Run("returns 404 for non-existent policy", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/policies/00000000-0000-0000-0000-000000000001", e.adminToken(t))
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 400 for malformed UUID", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/policies/bad-uuid", e.adminToken(t))
		assertStatus(t, resp, http.StatusBadRequest)
	})
}

func TestPolicyHandler_Create(t *testing.T) {
	validPolicy := func(agentID string) map[string]any {
		return map[string]any{
			"name":          "backup-policy",
			"agent_id":      agentID,
			"schedule":      "@daily",
			"sources":       `["/data"]`,
			"repo_password": "supersecret",
		}
	}

	t.Run("creates policy and returns 201", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := uuid.New().String()

		resp := e.post(t, "/api/v1/policies", e.adminToken(t), validPolicy(agentID))
		assertStatus(t, resp, http.StatusCreated)

		var data struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Schedule string `json:"schedule"`
			Enabled  bool   `json:"enabled"`
		}
		decodeData(t, resp, &data)
		if data.Name != "backup-policy" {
			t.Errorf("name = %q, want backup-policy", data.Name)
		}
		if !data.Enabled {
			t.Error("enabled = false, want true (default)")
		}
		if data.ID == "" {
			t.Error("id is empty")
		}
	})

	t.Run("returns 400 when name is missing", func(t *testing.T) {
		e := newTestEnv(t)
		body := validPolicy(uuid.New().String())
		delete(body, "name")
		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body)
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when agent_id is missing", func(t *testing.T) {
		e := newTestEnv(t)
		body := validPolicy(uuid.New().String())
		delete(body, "agent_id")
		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body)
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when schedule is missing", func(t *testing.T) {
		e := newTestEnv(t)
		body := validPolicy(uuid.New().String())
		delete(body, "schedule")
		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body)
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when schedule is invalid cron", func(t *testing.T) {
		e := newTestEnv(t)
		body := validPolicy(uuid.New().String())
		body["schedule"] = "not-a-cron-expression"
		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body)
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when sources is missing", func(t *testing.T) {
		e := newTestEnv(t)
		body := validPolicy(uuid.New().String())
		delete(body, "sources")
		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body)
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when repo_password is missing", func(t *testing.T) {
		e := newTestEnv(t)
		body := validPolicy(uuid.New().String())
		delete(body, "repo_password")
		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body)
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 403 when non-admin sets hook_pre_backup", func(t *testing.T) {
		e := newTestEnv(t)
		body := validPolicy(uuid.New().String())
		body["hook_pre_backup"] = "/usr/local/bin/pre-backup.sh"
		resp := e.post(t, "/api/v1/policies", e.userToken(t), body)
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("returns 400 when hook_pre_backup contains shell injection", func(t *testing.T) {
		e := newTestEnv(t)
		body := validPolicy(uuid.New().String())
		body["hook_pre_backup"] = "echo $(cat /etc/passwd)"
		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body)
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/policies", "", validPolicy(uuid.New().String()))
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestPolicyHandler_Update(t *testing.T) {
	t.Run("updates policy name", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := uuid.New()
		policy := createDBPolicy(t, e.deps, "original", agentID)

		name := "updated"
		resp := e.patch(t, "/api/v1/policies/"+policy.ID.String(), e.adminToken(t), map[string]any{
			"name": &name,
		})
		assertStatus(t, resp, http.StatusOK)

		var data struct{ Name string `json:"name"` }
		decodeData(t, resp, &data)
		if data.Name != "updated" {
			t.Errorf("name = %q, want updated", data.Name)
		}
	})

	t.Run("returns 400 when setting empty name", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := uuid.New()
		policy := createDBPolicy(t, e.deps, "policy", agentID)

		empty := ""
		resp := e.patch(t, "/api/v1/policies/"+policy.ID.String(), e.adminToken(t), map[string]any{
			"name": &empty,
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when schedule is invalid", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := uuid.New()
		policy := createDBPolicy(t, e.deps, "policy", agentID)

		bad := "not-cron"
		resp := e.patch(t, "/api/v1/policies/"+policy.ID.String(), e.adminToken(t), map[string]any{
			"schedule": &bad,
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 404 for non-existent policy", func(t *testing.T) {
		e := newTestEnv(t)
		name := "x"
		resp := e.patch(t, "/api/v1/policies/00000000-0000-0000-0000-000000000001", e.adminToken(t), map[string]any{
			"name": &name,
		})
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 400 when hook contains path traversal", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := uuid.New()
		policy := createDBPolicy(t, e.deps, "policy", agentID)

		hook := "cat ../../etc/passwd"
		resp := e.patch(t, "/api/v1/policies/"+policy.ID.String(), e.adminToken(t), map[string]any{
			"hook_pre_backup": &hook,
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})
}

func TestPolicyHandler_Delete(t *testing.T) {
	t.Run("admin can delete policy", func(t *testing.T) {
		e := newTestEnv(t)
		policy := createDBPolicy(t, e.deps, "to-delete", uuid.New())

		resp := e.del(t, "/api/v1/policies/"+policy.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusNoContent)
	})

	t.Run("returns 403 for non-admin user", func(t *testing.T) {
		e := newTestEnv(t)
		policy := createDBPolicy(t, e.deps, "protected", uuid.New())

		resp := e.del(t, "/api/v1/policies/"+policy.ID.String(), e.userToken(t))
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("returns 404 for non-existent policy", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.del(t, "/api/v1/policies/00000000-0000-0000-0000-000000000001", e.adminToken(t))
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.del(t, "/api/v1/policies/00000000-0000-0000-0000-000000000001", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}
