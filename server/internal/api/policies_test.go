package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// createDBPolicy inserts a policy record directly and returns it.
func createDBPolicy(t *testing.T, deps *testDeps, name string, agentID uuid.UUID) *db.Policy {
	t.Helper()
	p := &db.Policy{
		Name:         name,
		AgentID:      agentID,
		Schedule:     "@daily",
		Enabled:      true,
		Sources:      `[{"type":"directory","path":"/data"}]`,
		RepoPassword: "secret",
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
			"sources":       `[{"type":"directory","path":"/data"}]`,
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

	t.Run("returns 400 when a source is flag-like, even for an admin", func(t *testing.T) {
		// Regression test for GHSA-263g-c333-jcjq / GHSA-75rg-4ppf-pq7g: sources
		// are rejected for looking like a restic flag, not gated by admin
		// status like hooks are — an admin token must be rejected too, proving
		// this is a validation rule and not a privilege check.
		e := newTestEnv(t)
		body := validPolicy(uuid.New().String())
		body["sources"] = `[{"type":"directory","path":"--password-command=touch /tmp/pwned"}]`
		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body)
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 403 for a non-admin", func(t *testing.T) {
		// The user role is read-only: a policy decides what an agent backs up
		// and where the data goes.
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID
		resp := e.post(t, "/api/v1/policies", e.userToken(t), validPolicy(agentID.String()))
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("returns 403 when non-admin sets hook_pre_backup", func(t *testing.T) {
		e := newTestEnv(t)
		body := validPolicy(uuid.New().String())
		body["hook_pre_backup"] = "/usr/local/bin/pre-backup.sh"
		resp := e.post(t, "/api/v1/policies", e.userToken(t), body)
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("returns 403 when non-admin sets a command source", func(t *testing.T) {
		e := newTestEnv(t)
		body := validPolicy(uuid.New().String())
		body["sources"] = `[{"type":"command","path":"pg_dump mydb","label":"pgdump"}]`
		resp := e.post(t, "/api/v1/policies", e.userToken(t), body)
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("returns 201 when admin sets a command source", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID.String()
		body := validPolicy(agentID)
		body["sources"] = `[{"type":"command","path":"pg_dump mydb","label":"pgdump"}]`
		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body)
		assertStatus(t, resp, http.StatusCreated)
	})

	t.Run("returns 400 when a command source has an invalid name", func(t *testing.T) {
		e := newTestEnv(t)
		body := validPolicy(uuid.New().String())
		body["sources"] = `[{"type":"command","path":"pg_dump mydb","label":"has space"}]`
		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body)
		assertStatus(t, resp, http.StatusBadRequest)
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

	t.Run("use_destination_password resolves the password from an s3 destination too", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID.String()
		dest := createDBDestination(t, e.deps, "imported-s3", "s3")
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

	t.Run("use_destination_password fails when an s3 and an rclone destination disagree", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID.String()
		destA := createDBDestination(t, e.deps, "imported-s3", "s3")
		destA.RepoPassword = "password-a"
		if err := e.deps.dests.Update(context.Background(), destA); err != nil {
			t.Fatalf("Update destA: %v", err)
		}
		destB := createDBDestination(t, e.deps, "imported-rclone", "rclone")
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

		var data struct {
			Name string `json:"name"`
		}
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

	t.Run("returns 400 when updating with a flag-like source, storage unchanged", func(t *testing.T) {
		// Regression test for GHSA-263g-c333-jcjq / GHSA-75rg-4ppf-pq7g.
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID
		policy := createDBPolicy(t, e.deps, "policy", agentID)

		badSources := `[{"type":"directory","path":"--password-command=touch /tmp/pwned"}]`
		resp := e.patch(t, "/api/v1/policies/"+policy.ID.String(), e.adminToken(t), map[string]any{
			"sources": &badSources,
		})
		assertStatus(t, resp, http.StatusBadRequest)

		getResp := e.get(t, "/api/v1/policies/"+policy.ID.String(), e.adminToken(t))
		assertStatus(t, getResp, http.StatusOK)
		var data struct {
			Sources string `json:"sources"`
		}
		decodeData(t, getResp, &data)
		if data.Sources != policy.Sources {
			t.Errorf("sources = %q, want unchanged %q", data.Sources, policy.Sources)
		}
	})

	t.Run("returns 403 when a non-admin updates a policy", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID
		policy := createDBPolicy(t, e.deps, "policy", agentID)

		newSources := `[{"type":"directory","path":"/var/backups"}]`
		resp := e.patch(t, "/api/v1/policies/"+policy.ID.String(), e.userToken(t), map[string]any{
			"sources": &newSources,
		})
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("returns 403 when non-admin adds a command source", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID
		policy := createDBPolicy(t, e.deps, "policy", agentID)

		newSources := `[{"type":"directory","path":"/data"},{"type":"command","path":"pg_dump mydb","label":"pgdump"}]`
		resp := e.patch(t, "/api/v1/policies/"+policy.ID.String(), e.userToken(t), map[string]any{
			"sources": &newSources,
		})
		assertStatus(t, resp, http.StatusForbidden)
	})
}

func TestPolicyHandler_Delete(t *testing.T) {
	t.Run("admin can delete policy", func(t *testing.T) {
		e := newTestEnv(t)
		policy := createDBPolicy(t, e.deps, "to-delete", createDBAgent(t, e.deps, "test-agent").ID)

		resp := e.del(t, "/api/v1/policies/"+policy.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusNoContent)
	})

	t.Run("wipes the stored repository password", func(t *testing.T) {
		e := newTestEnv(t)
		policy := createDBPolicy(t, e.deps, "to-wipe", createDBAgent(t, e.deps, "test-agent").ID)

		resp := e.del(t, "/api/v1/policies/"+policy.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusNoContent)

		var row struct {
			RepoPassword string
			Deleted      bool
		}
		if err := e.deps.gdb.Raw(`SELECT repo_password, deleted_at IS NOT NULL AS deleted FROM policies WHERE id = ?`, policy.ID).
			Scan(&row).Error; err != nil {
			t.Fatalf("read policy row: %v", err)
		}
		if !row.Deleted {
			t.Error("deleted_at is NULL, want the row soft-deleted")
		}
		if row.RepoPassword != "" {
			t.Errorf("repo_password = %q, want wiped", row.RepoPassword)
		}

		// A second delete of the same policy is a 404, not a re-wipe.
		resp = e.del(t, "/api/v1/policies/"+policy.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusNotFound)
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

// TestPolicyHandler_Trigger locks the message shown by the GUI when "Run now"
// is used on a disabled policy (#287): it must tell the user how to fix it.
func TestPolicyHandler_Trigger(t *testing.T) {
	t.Run("returns 409 with an actionable message for a disabled policy", func(t *testing.T) {
		e := newTestEnv(t)
		policy := createDBPolicy(t, e.deps, "disabled", createDBAgent(t, e.deps, "test-agent").ID)
		policy.Enabled = false
		if err := e.deps.policies.Update(context.Background(), policy); err != nil {
			t.Fatalf("disable policy: %v", err)
		}

		resp := e.post(t, "/api/v1/policies/"+policy.ID.String()+"/trigger", e.adminToken(t), nil)
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusConflict)
		}
		var body struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if want := "This policy is disabled. Enable it to run a backup."; body.Error.Message != want {
			t.Errorf("message = %q, want %q", body.Error.Message, want)
		}
	})

	t.Run("returns 404 for non-existent policy", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/policies/00000000-0000-0000-0000-000000000001/trigger", e.adminToken(t), nil)
		assertStatus(t, resp, http.StatusNotFound)
	})
}

// TestPolicyHandler_ResumeInterrupted locks the create/update contract for the
// resume option. Worth its own test because db.Policy.ResumeInterrupted carries
// no GORM default tag: were one added, GORM would drop an explicit false from the
// INSERT and silently turn it into true.
func TestPolicyHandler_ResumeInterrupted(t *testing.T) {
	body := func(agentID string) map[string]any {
		return map[string]any{
			"name":          "laptop-policy",
			"agent_id":      agentID,
			"schedule":      "@daily",
			"sources":       `[{"type":"directory","path":"/data"}]`,
			"repo_password": "supersecret",
		}
	}
	type policyBody struct {
		ID                string `json:"id"`
		ResumeInterrupted bool   `json:"resume_interrupted"`
	}

	t.Run("defaults to enabled when omitted", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID.String()

		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body(agentID))
		assertStatus(t, resp, http.StatusCreated)

		var data policyBody
		decodeData(t, resp, &data)
		if !data.ResumeInterrupted {
			t.Error("resume_interrupted = false, want true when the field is omitted")
		}
	})

	t.Run("honours an explicit false on create", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID.String()
		b := body(agentID)
		b["resume_interrupted"] = false

		resp := e.post(t, "/api/v1/policies", e.adminToken(t), b)
		assertStatus(t, resp, http.StatusCreated)

		var data policyBody
		decodeData(t, resp, &data)
		if data.ResumeInterrupted {
			t.Error("resume_interrupted = true, want false as requested")
		}

		// And it survives a round trip through the database.
		stored, err := e.deps.policies.GetByID(context.Background(), mustParsePolicyUUID(t, data.ID))
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if stored.ResumeInterrupted {
			t.Error("stored policy has resume_interrupted = true, want false")
		}
	})

	t.Run("can be toggled off and back on", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID.String()
		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body(agentID))
		assertStatus(t, resp, http.StatusCreated)
		var created policyBody
		decodeData(t, resp, &created)

		for _, want := range []bool{false, true} {
			resp = e.patch(t, "/api/v1/policies/"+created.ID, e.adminToken(t), map[string]any{
				"resume_interrupted": want,
			})
			assertStatus(t, resp, http.StatusOK)

			var updated policyBody
			decodeData(t, resp, &updated)
			if updated.ResumeInterrupted != want {
				t.Errorf("resume_interrupted = %v after PATCH, want %v", updated.ResumeInterrupted, want)
			}
		}
	})
}

// TestPolicyHandler_NotifyOverride locks the create/update contract for the
// per-policy notification override.
func TestPolicyHandler_NotifyOverride(t *testing.T) {
	body := func(agentID string) map[string]any {
		return map[string]any{
			"name":          "notify-policy",
			"agent_id":      agentID,
			"schedule":      "@daily",
			"sources":       `[{"type":"directory","path":"/data"}]`,
			"repo_password": "supersecret",
		}
	}
	type policyBody struct {
		ID              string `json:"id"`
		NotifyOnSuccess string `json:"notify_on_success"`
		NotifyOnFailure string `json:"notify_on_failure"`
	}

	t.Run("defaults to inherit when omitted", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID.String()

		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body(agentID))
		assertStatus(t, resp, http.StatusCreated)

		var data policyBody
		decodeData(t, resp, &data)
		if data.NotifyOnSuccess != db.NotifyInherit || data.NotifyOnFailure != db.NotifyInherit {
			t.Errorf("overrides = %q/%q, want inherit/inherit", data.NotifyOnSuccess, data.NotifyOnFailure)
		}
	})

	t.Run("stores explicit values on create", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID.String()
		b := body(agentID)
		b["notify_on_success"] = db.NotifyAlways
		b["notify_on_failure"] = db.NotifyNever

		resp := e.post(t, "/api/v1/policies", e.adminToken(t), b)
		assertStatus(t, resp, http.StatusCreated)

		var data policyBody
		decodeData(t, resp, &data)
		stored, err := e.deps.policies.GetByID(context.Background(), mustParsePolicyUUID(t, data.ID))
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if stored.NotifyOnSuccess != db.NotifyAlways || stored.NotifyOnFailure != db.NotifyNever {
			t.Errorf("stored overrides = %q/%q, want always/never", stored.NotifyOnSuccess, stored.NotifyOnFailure)
		}
	})

	t.Run("can be changed and reset to inherit", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID.String()
		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body(agentID))
		assertStatus(t, resp, http.StatusCreated)
		var created policyBody
		decodeData(t, resp, &created)

		for _, want := range []string{db.NotifyNever, db.NotifyInherit} {
			resp = e.patch(t, "/api/v1/policies/"+created.ID, e.adminToken(t), map[string]any{
				"notify_on_success": want,
				"notify_on_failure": want,
			})
			assertStatus(t, resp, http.StatusOK)

			var updated policyBody
			decodeData(t, resp, &updated)
			if updated.NotifyOnSuccess != want || updated.NotifyOnFailure != want {
				t.Errorf("overrides = %q/%q after PATCH, want %q", updated.NotifyOnSuccess, updated.NotifyOnFailure, want)
			}
		}
	})

	t.Run("rejects an unknown value", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID.String()
		b := body(agentID)
		b["notify_on_success"] = "sometimes"
		assertStatus(t, e.post(t, "/api/v1/policies", e.adminToken(t), b), http.StatusBadRequest)

		resp := e.post(t, "/api/v1/policies", e.adminToken(t), body(agentID))
		assertStatus(t, resp, http.StatusCreated)
		var created policyBody
		decodeData(t, resp, &created)
		resp = e.patch(t, "/api/v1/policies/"+created.ID, e.adminToken(t), map[string]any{
			"notify_on_failure": "",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})
}

func mustParsePolicyUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse policy id %q: %v", s, err)
	}
	return id
}

// TestPolicyHandler_HealthcheckURL locks the create/update contract for the
// per-policy Healthchecks ping URL (issue #294): validated, admin-only.
func TestPolicyHandler_HealthcheckURL(t *testing.T) {
	const pingURL = "https://hc-ping.com/5f0c9f9e-1f1e-4e5b-9c7a-1b2c3d4e5f60"
	body := func(agentID string) map[string]any {
		return map[string]any{
			"name":          "hc-policy",
			"agent_id":      agentID,
			"schedule":      "@daily",
			"sources":       `[{"type":"directory","path":"/data"}]`,
			"repo_password": "supersecret",
		}
	}
	type policyBody struct {
		ID             string `json:"id"`
		HealthcheckURL string `json:"healthcheck_url"`
	}

	t.Run("admin sets it on create", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID.String()
		b := body(agentID)
		b["healthcheck_url"] = pingURL

		resp := e.post(t, "/api/v1/policies", e.adminToken(t), b)
		assertStatus(t, resp, http.StatusCreated)
		var data policyBody
		decodeData(t, resp, &data)
		if data.HealthcheckURL != pingURL {
			t.Errorf("healthcheck_url = %q, want %q", data.HealthcheckURL, pingURL)
		}
	})

	t.Run("non-admin cannot set it on create", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID.String()
		b := body(agentID)
		b["healthcheck_url"] = pingURL
		assertStatus(t, e.post(t, "/api/v1/policies", e.userToken(t), b), http.StatusForbidden)
	})

	t.Run("rejects an invalid URL", func(t *testing.T) {
		e := newTestEnv(t)
		agentID := createDBAgent(t, e.deps, "test-agent").ID.String()
		b := body(agentID)
		b["healthcheck_url"] = "hc-ping.com/abc"
		assertStatus(t, e.post(t, "/api/v1/policies", e.adminToken(t), b), http.StatusBadRequest)

		p := createDBPolicy(t, e.deps, "p", mustParsePolicyUUID(t, agentID))
		resp := e.patch(t, "/api/v1/policies/"+p.ID.String(), e.adminToken(t), map[string]any{
			"healthcheck_url": "ftp://hc-ping.com/abc",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("update is admin-only", func(t *testing.T) {
		e := newTestEnv(t)
		agent := createDBAgent(t, e.deps, "test-agent")
		p := createDBPolicy(t, e.deps, "p", agent.ID)
		path := "/api/v1/policies/" + p.ID.String()

		assertStatus(t, e.patch(t, path, e.userToken(t), map[string]any{"healthcheck_url": pingURL}), http.StatusForbidden)

		resp := e.patch(t, path, e.adminToken(t), map[string]any{"healthcheck_url": pingURL})
		assertStatus(t, resp, http.StatusOK)
		var data policyBody
		decodeData(t, resp, &data)
		if data.HealthcheckURL != pingURL {
			t.Errorf("healthcheck_url = %q after PATCH, want %q", data.HealthcheckURL, pingURL)
		}

		resp = e.patch(t, path, e.adminToken(t), map[string]any{"healthcheck_url": ""})
		assertStatus(t, resp, http.StatusOK)
		decodeData(t, resp, &data)
		if data.HealthcheckURL != "" {
			t.Errorf("healthcheck_url = %q after clearing, want empty", data.HealthcheckURL)
		}
	})
}

// TestPolicyHandler_TestHealthcheck covers the policy form's "Send test ping".
func TestPolicyHandler_TestHealthcheck(t *testing.T) {
	var gotPath string
	status := http.StatusOK
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(status)
	}))
	t.Cleanup(target.Close)

	e := newTestEnv(t)
	const path = "/api/v1/policies/healthcheck/test"

	assertStatus(t, e.post(t, path, e.userToken(t), map[string]any{"url": target.URL + "/abc"}), http.StatusForbidden)
	assertStatus(t, e.post(t, path, e.adminToken(t), map[string]any{"url": "not a url"}), http.StatusBadRequest)

	assertStatus(t, e.post(t, path, e.adminToken(t), map[string]any{"url": target.URL + "/abc"}), http.StatusOK)
	if gotPath != "/abc/log" {
		t.Errorf("test ping path = %q, want /abc/log", gotPath)
	}

	status = http.StatusNotFound
	assertStatus(t, e.post(t, path, e.adminToken(t), map[string]any{"url": target.URL + "/abc"}), http.StatusUnprocessableEntity)
}
