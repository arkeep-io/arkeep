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
		agentID := createDBAgent(t, e.deps, "test-agent").ID
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
		agentID := createDBAgent(t, e.deps, "test-agent").ID
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
		agentID := createDBAgent(t, e.deps, "test-agent").ID.String()

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

	t.Run("preserves zero retention values", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID.String()
		body := validPolicy(agentID)
		body["retention_daily"] = 0
		body["retention_yearly"] = 0

		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body)
		assertStatus(t, resp, http.StatusCreated)

		var created struct {
			ID             string `json:"id"`
			RetentionDaily int    `json:"retention_daily"`
			RetentionYearly int   `json:"retention_yearly"`
		}
		decodeData(t, resp, &created)
		if created.RetentionDaily != 0 {
			t.Errorf("create: retention_daily = %d, want 0", created.RetentionDaily)
		}
		if created.RetentionYearly != 0 {
			t.Errorf("create: retention_yearly = %d, want 0", created.RetentionYearly)
		}

		resp2 := e.get(t, "/api/v1/policies/"+created.ID, e.adminToken(t))
		assertStatus(t, resp2, http.StatusOK)
		var fetched struct {
			RetentionDaily  int `json:"retention_daily"`
			RetentionYearly int `json:"retention_yearly"`
		}
		decodeData(t, resp2, &fetched)
		if fetched.RetentionDaily != 0 {
			t.Errorf("fetch: retention_daily = %d, want 0", fetched.RetentionDaily)
		}
		if fetched.RetentionYearly != 0 {
			t.Errorf("fetch: retention_yearly = %d, want 0", fetched.RetentionYearly)
		}
	})

	t.Run("use_destination_password resolves the password from the destination, never from the request", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID.String()
		dest := createDBDestination(t, e.deps, "imported", "rclone")
		dest.RepoPassword = "captured-at-import"
		if err := e.deps.dests.Update(context.Background(), dest); err != nil {
			t.Fatalf("Update: %v", err)
		}

		body := validPolicy(agentID)
		delete(body, "repo_password")
		body["use_destination_password"] = true
		body["destinations"] = []map[string]any{{"destination_id": dest.ID.String(), "priority": 0}}

		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body)
		assertStatus(t, resp, http.StatusCreated)

		var created struct {
			ID string `json:"id"`
		}
		decodeData(t, resp, &created)
		id, err := uuid.Parse(created.ID)
		if err != nil {
			t.Fatalf("parse id: %v", err)
		}
		policy, err := e.deps.policies.GetByID(context.Background(), id)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if string(policy.RepoPassword) != "captured-at-import" {
			t.Errorf("RepoPassword = %q, want the destination's stored password", policy.RepoPassword)
		}
	})

	t.Run("use_destination_password fails when the destination has no stored password", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID.String()
		dest := createDBDestination(t, e.deps, "fresh", "rclone")

		body := validPolicy(agentID)
		delete(body, "repo_password")
		body["use_destination_password"] = true
		body["destinations"] = []map[string]any{{"destination_id": dest.ID.String(), "priority": 0}}

		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body)
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("use_destination_password fails when selected destinations disagree", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID.String()
		destA := createDBDestination(t, e.deps, "imported-a", "rclone")
		destA.RepoPassword = "password-a"
		if err := e.deps.dests.Update(context.Background(), destA); err != nil {
			t.Fatalf("Update destA: %v", err)
		}
		destB := createDBDestination(t, e.deps, "imported-b", "rclone")
		destB.RepoPassword = "password-b"
		if err := e.deps.dests.Update(context.Background(), destB); err != nil {
			t.Fatalf("Update destB: %v", err)
		}

		body := validPolicy(agentID)
		delete(body, "repo_password")
		body["use_destination_password"] = true
		body["destinations"] = []map[string]any{
			{"destination_id": destA.ID.String(), "priority": 0},
			{"destination_id": destB.ID.String(), "priority": 1},
		}

		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body)
		assertStatus(t, resp, http.StatusBadRequest)
	})
}

func TestPolicyHandler_Update(t *testing.T) {
	t.Run("updates policy name", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID
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
		agentID := createDBAgent(t, e.deps, "test-agent").ID
		policy := createDBPolicy(t, e.deps, "policy", agentID)

		empty := ""
		resp := e.patch(t, "/api/v1/policies/"+policy.ID.String(), e.adminToken(t), map[string]any{
			"name": &empty,
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when schedule is invalid", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID
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
		agentID := createDBAgent(t, e.deps, "test-agent").ID
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
		policy := createDBPolicy(t, e.deps, "to-delete", createDBAgent(t, e.deps, "test-agent").ID)

		resp := e.del(t, "/api/v1/policies/"+policy.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusNoContent)
	})

	t.Run("returns 403 for non-admin user", func(t *testing.T) {
		e := newTestEnv(t)
		policy := createDBPolicy(t, e.deps, "protected", createDBAgent(t, e.deps, "test-agent").ID)

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
