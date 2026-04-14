package api

import (
	"fmt"
	"net/http"
	"testing"
)

func TestAgentHandler_List(t *testing.T) {
	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/agents", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("returns empty list on fresh DB", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/agents", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		}
		decodeData(t, resp, &data)
		if len(data.Items) != 0 {
			t.Errorf("items len = %d, want 0", len(data.Items))
		}
		if data.Total != 0 {
			t.Errorf("total = %d, want 0", data.Total)
		}
	})

	t.Run("returns agents after creation", func(t *testing.T) {
		e := newTestEnv(t)
		createDBAgent(t, e.deps, "agent-alpha")
		createDBAgent(t, e.deps, "agent-beta")

		resp := e.get(t, "/api/v1/agents", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []struct{ Name string `json:"name"` } `json:"items"`
			Total int64                                 `json:"total"`
		}
		decodeData(t, resp, &data)
		if data.Total != 2 {
			t.Errorf("total = %d, want 2", data.Total)
		}
	})

	t.Run("respects limit parameter", func(t *testing.T) {
		e := newTestEnv(t)
		for i := 0; i < 5; i++ {
			createDBAgent(t, e.deps, fmt.Sprintf("agent-%d", i))
		}

		resp := e.get(t, "/api/v1/agents?limit=2", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		}
		decodeData(t, resp, &data)
		if len(data.Items) != 2 {
			t.Errorf("items len = %d, want 2 (limit=2)", len(data.Items))
		}
		if data.Total != 5 {
			t.Errorf("total = %d, want 5", data.Total)
		}
	})
}

func TestAgentHandler_Create(t *testing.T) {
	t.Run("returns 201 with valid name", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/agents", e.adminToken(t), map[string]string{
			"name": "my-backup-agent",
		})
		assertStatus(t, resp, http.StatusCreated)

		var data struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		}
		decodeData(t, resp, &data)
		if data.Name != "my-backup-agent" {
			t.Errorf("name = %q, want %q", data.Name, "my-backup-agent")
		}
		if data.Status != "offline" {
			t.Errorf("status = %q, want offline", data.Status)
		}
		if data.ID == "" {
			t.Error("id is empty")
		}
	})

	t.Run("returns 400 when name is empty", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/agents", e.adminToken(t), map[string]string{
			"name": "",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 on unknown fields", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/agents", e.adminToken(t), map[string]string{
			"name":    "agent",
			"unknown": "field",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/agents", "", map[string]string{"name": "agent"})
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestAgentHandler_GetByID(t *testing.T) {
	t.Run("returns agent by UUID", func(t *testing.T) {
		e := newTestEnv(t)
		agent := createDBAgent(t, e.deps, "test-agent")

		resp := e.get(t, "/api/v1/agents/"+agent.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		decodeData(t, resp, &data)
		if data.ID != agent.ID.String() {
			t.Errorf("id = %q, want %q", data.ID, agent.ID.String())
		}
		if data.Name != "test-agent" {
			t.Errorf("name = %q, want test-agent", data.Name)
		}
	})

	t.Run("returns 404 for non-existent UUID", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/agents/00000000-0000-0000-0000-000000000001", e.adminToken(t))
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 400 for malformed UUID", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/agents/not-a-uuid", e.adminToken(t))
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/agents/00000000-0000-0000-0000-000000000001", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestAgentHandler_Update(t *testing.T) {
	t.Run("updates agent name successfully", func(t *testing.T) {
		e := newTestEnv(t)
		agent := createDBAgent(t, e.deps, "original-name")

		name := "updated-name"
		resp := e.patch(t, "/api/v1/agents/"+agent.ID.String(), e.adminToken(t), map[string]any{
			"name": name,
		})
		assertStatus(t, resp, http.StatusOK)

		var data struct{ Name string `json:"name"` }
		decodeData(t, resp, &data)
		if data.Name != "updated-name" {
			t.Errorf("name = %q, want updated-name", data.Name)
		}
	})

	t.Run("returns 400 when setting empty name", func(t *testing.T) {
		e := newTestEnv(t)
		agent := createDBAgent(t, e.deps, "agent")

		empty := ""
		resp := e.patch(t, "/api/v1/agents/"+agent.ID.String(), e.adminToken(t), map[string]any{
			"name": &empty,
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 404 for non-existent agent", func(t *testing.T) {
		e := newTestEnv(t)
		name := "new-name"
		resp := e.patch(t, "/api/v1/agents/00000000-0000-0000-0000-000000000001", e.adminToken(t), map[string]any{
			"name": &name,
		})
		assertStatus(t, resp, http.StatusNotFound)
	})
}

func TestAgentHandler_Delete(t *testing.T) {
	t.Run("admin can delete agent", func(t *testing.T) {
		e := newTestEnv(t)
		agent := createDBAgent(t, e.deps, "to-delete")

		resp := e.del(t, "/api/v1/agents/"+agent.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusNoContent)
	})

	t.Run("returns 403 for non-admin user", func(t *testing.T) {
		e := newTestEnv(t)
		agent := createDBAgent(t, e.deps, "protected-agent")

		resp := e.del(t, "/api/v1/agents/"+agent.ID.String(), e.userToken(t))
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("returns 404 for non-existent agent", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.del(t, "/api/v1/agents/00000000-0000-0000-0000-000000000001", e.adminToken(t))
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.del(t, "/api/v1/agents/00000000-0000-0000-0000-000000000001", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestAgentHandler_ListVolumes(t *testing.T) {
	t.Run("returns 409 when agent is not connected", func(t *testing.T) {
		e := newTestEnv(t)
		agent := createDBAgent(t, e.deps, "offline-agent")

		resp := e.get(t, "/api/v1/agents/"+agent.ID.String()+"/volumes", e.adminToken(t))
		assertStatus(t, resp, http.StatusConflict)
	})
}
