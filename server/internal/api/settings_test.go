package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/arkeep-io/arkeep/server/internal/notification"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

func TestSettingsHandler_ListOIDC(t *testing.T) {
	t.Run("admin can list OIDC providers", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/settings/oidc", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data []any
		decodeData(t, resp, &data)
		if data == nil {
			t.Error("data is nil, want empty array")
		}
	})

	t.Run("returns 403 for non-admin", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/settings/oidc", e.userToken(t))
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/settings/oidc", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestSettingsHandler_CreateOIDC(t *testing.T) {
	validOIDC := map[string]any{
		"name":          "Test IdP",
		"issuer":        "https://idp.example.com",
		"client_id":     "my-client-id",
		"client_secret": "super-secret",
		"enabled":       false,
	}

	t.Run("admin creates OIDC provider", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/settings/oidc", e.adminToken(t), validOIDC)
		assertStatus(t, resp, http.StatusCreated)

		var data struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Issuer  string `json:"issuer"`
			Enabled bool   `json:"enabled"`
		}
		decodeData(t, resp, &data)
		if data.Name != "Test IdP" {
			t.Errorf("name = %q, want Test IdP", data.Name)
		}
		if data.Issuer != "https://idp.example.com" {
			t.Errorf("issuer = %q, want https://idp.example.com", data.Issuer)
		}
	})

	t.Run("returns 400 when name is missing", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/settings/oidc", e.adminToken(t), map[string]any{
			"issuer":        "https://idp.example.com",
			"client_id":     "id",
			"client_secret": "secret",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when issuer is missing", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/settings/oidc", e.adminToken(t), map[string]any{
			"name":          "Provider",
			"client_id":     "id",
			"client_secret": "secret",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when client_id is missing", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/settings/oidc", e.adminToken(t), map[string]any{
			"name":          "Provider",
			"issuer":        "https://idp.example.com",
			"client_secret": "secret",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when client_secret is missing", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/settings/oidc", e.adminToken(t), map[string]any{
			"name":      "Provider",
			"issuer":    "https://idp.example.com",
			"client_id": "id",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 403 for non-admin", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/settings/oidc", e.userToken(t), validOIDC)
		assertStatus(t, resp, http.StatusForbidden)
	})
}

func TestSettingsHandler_GetOIDCByID(t *testing.T) {
	t.Run("returns OIDC provider by ID", func(t *testing.T) {
		e := newTestEnv(t)
		// First create one.
		createResp := e.post(t, "/api/v1/settings/oidc", e.adminToken(t), map[string]any{
			"name":          "My IdP",
			"issuer":        "https://idp.example.com",
			"client_id":     "cid",
			"client_secret": "sec",
		})
		assertStatus(t, createResp, http.StatusCreated)
		var created struct{ ID string `json:"id"` }
		decodeData(t, createResp, &created)

		resp := e.get(t, "/api/v1/settings/oidc/"+created.ID, e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct{ ID string `json:"id"` }
		decodeData(t, resp, &data)
		if data.ID != created.ID {
			t.Errorf("id = %q, want %q", data.ID, created.ID)
		}
	})

	t.Run("returns 404 for non-existent provider", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/settings/oidc/00000000-0000-0000-0000-000000000001", e.adminToken(t))
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 403 for non-admin", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/settings/oidc/00000000-0000-0000-0000-000000000001", e.userToken(t))
		assertStatus(t, resp, http.StatusForbidden)
	})
}

func TestSettingsHandler_UpdateOIDC(t *testing.T) {
	t.Run("updates OIDC provider", func(t *testing.T) {
		e := newTestEnv(t)
		createResp := e.post(t, "/api/v1/settings/oidc", e.adminToken(t), map[string]any{
			"name":          "Old Name",
			"issuer":        "https://idp.example.com",
			"client_id":     "cid",
			"client_secret": "sec",
		})
		assertStatus(t, createResp, http.StatusCreated)
		var created struct{ ID string `json:"id"` }
		decodeData(t, createResp, &created)

		// Use doJSON for PUT.
		req := map[string]any{
			"name":      "New Name",
			"issuer":    "https://idp.example.com",
			"client_id": "cid",
			"enabled":   true,
		}
		resp := e.doJSON(t, "PUT", "/api/v1/settings/oidc/"+created.ID, e.adminToken(t), req)
		assertStatus(t, resp, http.StatusOK)

		var data struct{ Name string `json:"name"` }
		decodeData(t, resp, &data)
		if data.Name != "New Name" {
			t.Errorf("name = %q, want New Name", data.Name)
		}
	})

	t.Run("returns 404 for non-existent provider", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/oidc/00000000-0000-0000-0000-000000000001",
			e.adminToken(t), map[string]any{
				"name":      "Name",
				"issuer":    "https://idp.example.com",
				"client_id": "cid",
			})
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 403 for non-admin", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/oidc/00000000-0000-0000-0000-000000000001",
			e.userToken(t), map[string]any{
				"name":      "Name",
				"issuer":    "https://idp.example.com",
				"client_id": "cid",
			})
		assertStatus(t, resp, http.StatusForbidden)
	})
}

func TestSettingsHandler_DeleteOIDC(t *testing.T) {
	t.Run("deletes OIDC provider", func(t *testing.T) {
		e := newTestEnv(t)
		createResp := e.post(t, "/api/v1/settings/oidc", e.adminToken(t), map[string]any{
			"name":          "To Delete",
			"issuer":        "https://idp.example.com",
			"client_id":     "cid",
			"client_secret": "sec",
		})
		assertStatus(t, createResp, http.StatusCreated)
		var created struct{ ID string `json:"id"` }
		decodeData(t, createResp, &created)

		resp := e.del(t, "/api/v1/settings/oidc/"+created.ID, e.adminToken(t))
		assertStatus(t, resp, http.StatusNoContent)
	})

	t.Run("returns 404 for non-existent provider", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.del(t, "/api/v1/settings/oidc/00000000-0000-0000-0000-000000000001", e.adminToken(t))
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 403 for non-admin", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.del(t, "/api/v1/settings/oidc/00000000-0000-0000-0000-000000000001", e.userToken(t))
		assertStatus(t, resp, http.StatusForbidden)
	})
}

func TestSettingsHandler_GetSMTP(t *testing.T) {
	t.Run("returns 404 when no SMTP configured", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/settings/smtp", e.adminToken(t))
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 403 for non-admin", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/settings/smtp", e.userToken(t))
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/settings/smtp", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestSettingsHandler_UpsertSMTP(t *testing.T) {
	validSMTP := map[string]any{
		"host":       "smtp.example.com",
		"port":       587,
		"username":   "user@example.com",
		"password":   "pass",
		"from":       "noreply@example.com",
		"from_name":  "My Backups",
		"tls":        true,
		"recipients": []string{"admin@example.com"},
	}

	t.Run("admin can upsert SMTP settings", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/smtp", e.adminToken(t), validSMTP)
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Host     string `json:"host"`
			Port     int    `json:"port"`
			Password string `json:"password"`
		}
		decodeData(t, resp, &data)
		if data.Host != "smtp.example.com" {
			t.Errorf("host = %q, want smtp.example.com", data.Host)
		}
		if data.Port != 587 {
			t.Errorf("port = %d, want 587", data.Port)
		}
		// Password must be masked.
		if data.Password != "***" {
			t.Errorf("password = %q, want ***", data.Password)
		}
	})

	t.Run("persists from_name and returns it via GET", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/smtp", e.adminToken(t), validSMTP)
		assertStatus(t, resp, http.StatusOK)

		resp = e.get(t, "/api/v1/settings/smtp", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)
		var data struct {
			FromName string `json:"from_name"`
		}
		decodeData(t, resp, &data)
		if data.FromName != "My Backups" {
			t.Errorf("from_name = %q, want %q", data.FromName, "My Backups")
		}
	})

	t.Run("returns 400 when host is missing", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/smtp", e.adminToken(t), map[string]any{
			"port": 587,
			"from": "noreply@example.com",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when port is out of range", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/smtp", e.adminToken(t), map[string]any{
			"host": "smtp.example.com",
			"port": 0,
			"from": "noreply@example.com",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when from is missing", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/smtp", e.adminToken(t), map[string]any{
			"host": "smtp.example.com",
			"port": 587,
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 403 for non-admin", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/smtp", e.userToken(t), validSMTP)
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/smtp", "", validSMTP)
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestSettingsHandler_GetWebhook(t *testing.T) {
	t.Run("returns 404 when no webhook configured", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/settings/webhook", e.adminToken(t))
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 403 for non-admin", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/settings/webhook", e.userToken(t))
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/settings/webhook", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestSettingsHandler_UpsertWebhook(t *testing.T) {
	validWebhook := map[string]any{
		"url":     "https://hooks.example.com/arkeep",
		"secret":  "hmac-secret",
		"enabled": true,
	}

	t.Run("admin can upsert webhook settings", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/webhook", e.adminToken(t), validWebhook)
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			URL     string `json:"url"`
			Secret  string `json:"secret"`
			Enabled bool   `json:"enabled"`
		}
		decodeData(t, resp, &data)
		if data.URL != "https://hooks.example.com/arkeep" {
			t.Errorf("url = %q, want https://hooks.example.com/arkeep", data.URL)
		}
		if !data.Enabled {
			t.Error("enabled = false, want true")
		}
		// Secret must be masked.
		if data.Secret != "***" {
			t.Errorf("secret = %q, want ***", data.Secret)
		}
	})

	t.Run("persists url and returns it via GET", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/webhook", e.adminToken(t), validWebhook)
		assertStatus(t, resp, http.StatusOK)

		resp = e.get(t, "/api/v1/settings/webhook", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)
		var data struct {
			URL     string `json:"url"`
			Enabled bool   `json:"enabled"`
		}
		decodeData(t, resp, &data)
		if data.URL != "https://hooks.example.com/arkeep" {
			t.Errorf("url = %q, want https://hooks.example.com/arkeep", data.URL)
		}
		if !data.Enabled {
			t.Error("enabled = false, want true")
		}
	})

	t.Run("blank secret keeps the existing one", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/webhook", e.adminToken(t), validWebhook)
		assertStatus(t, resp, http.StatusOK)

		resp = e.doJSON(t, "PUT", "/api/v1/settings/webhook", e.adminToken(t), map[string]any{
			"url":     "https://hooks.example.com/arkeep",
			"secret":  "",
			"enabled": true,
		})
		assertStatus(t, resp, http.StatusOK)

		settings, err := e.deps.settings.GetMany(context.Background(), "webhook.")
		if err != nil {
			t.Fatalf("GetMany: %v", err)
		}
		idx := settingsToMap(settings)
		if idx[notification.KeyWebhookSecret] != "hmac-secret" {
			t.Errorf("stored secret = %q, want it unchanged (hmac-secret)", idx[notification.KeyWebhookSecret])
		}
	})

	t.Run("returns 400 when enabled with a blank url", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/webhook", e.adminToken(t), map[string]any{
			"url":     "",
			"enabled": true,
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 for a malformed url", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/webhook", e.adminToken(t), map[string]any{
			"url":     "not-a-url",
			"enabled": true,
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 403 for non-admin", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/webhook", e.userToken(t), validWebhook)
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/webhook", "", validWebhook)
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestSettingsHandler_LogRetention(t *testing.T) {
	t.Run("defaults to zero (disabled)", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/settings/logs", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			InfoDays      int `json:"info_days"`
			WarnErrorDays int `json:"warn_error_days"`
		}
		decodeData(t, resp, &data)
		if data.InfoDays != 0 || data.WarnErrorDays != 0 {
			t.Errorf("got %+v, want both 0", data)
		}
	})

	t.Run("persists and returns via GET", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/logs", e.adminToken(t), map[string]any{
			"info_days":       30,
			"warn_error_days": 365,
		})
		assertStatus(t, resp, http.StatusOK)

		resp = e.get(t, "/api/v1/settings/logs", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)
		var data struct {
			InfoDays      int `json:"info_days"`
			WarnErrorDays int `json:"warn_error_days"`
		}
		decodeData(t, resp, &data)
		if data.InfoDays != 30 || data.WarnErrorDays != 365 {
			t.Errorf("got %+v, want {30 365}", data)
		}
	})

	t.Run("returns 400 for negative days", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/logs", e.adminToken(t), map[string]any{
			"info_days": -1,
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 403 for non-admin", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/settings/logs", e.userToken(t))
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("manual prune returns deleted count", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/settings/logs/prune", e.adminToken(t), nil)
		assertStatus(t, resp, http.StatusOK)
		var data struct {
			Deleted int64 `json:"deleted"`
		}
		decodeData(t, resp, &data)
		if data.Deleted != 0 {
			t.Errorf("deleted = %d, want 0 (no logs, retention disabled)", data.Deleted)
		}
	})
}

func TestSettingsHandler_SnapshotSync(t *testing.T) {
	type snapshotSync struct {
		IntervalHours int `json:"interval_hours"`
	}

	t.Run("defaults to zero (disabled)", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/settings/snapshot-sync", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data snapshotSync
		decodeData(t, resp, &data)
		if data.IntervalHours != 0 {
			t.Errorf("interval_hours = %d, want 0", data.IntervalHours)
		}
	})

	t.Run("persists and returns via GET", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.doJSON(t, "PUT", "/api/v1/settings/snapshot-sync", e.adminToken(t), map[string]any{
			"interval_hours": 24,
		})
		assertStatus(t, resp, http.StatusOK)

		resp = e.get(t, "/api/v1/settings/snapshot-sync", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)
		var data snapshotSync
		decodeData(t, resp, &data)
		if data.IntervalHours != 24 {
			t.Errorf("interval_hours = %d, want 24", data.IntervalHours)
		}
	})

	t.Run("returns 400 for an out-of-range interval", func(t *testing.T) {
		e := newTestEnv(t)
		for _, hours := range []int{-1, maxSnapshotSyncIntervalHours + 1} {
			resp := e.doJSON(t, "PUT", "/api/v1/settings/snapshot-sync", e.adminToken(t), map[string]any{
				"interval_hours": hours,
			})
			assertStatus(t, resp, http.StatusBadRequest)
		}
	})

	t.Run("returns 403 for non-admin", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/settings/snapshot-sync", e.userToken(t))
		assertStatus(t, resp, http.StatusForbidden)
	})
}

func TestSettingsHandler_JobQueue(t *testing.T) {
	type queueSettings struct {
		TimeoutMinutes int `json:"timeout_minutes"`
	}

	t.Run("defaults to 24 hours", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/settings/jobs-queue", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)
		var data queueSettings
		decodeData(t, resp, &data)
		if data.TimeoutMinutes != 1440 {
			t.Errorf("timeout_minutes = %d, want 1440", data.TimeoutMinutes)
		}
	})

	t.Run("persists and returns via GET, including 0", func(t *testing.T) {
		e := newTestEnv(t)
		for _, v := range []int{90, 0} {
			resp := e.doJSON(t, "PUT", "/api/v1/settings/jobs-queue", e.adminToken(t), map[string]any{"timeout_minutes": v})
			assertStatus(t, resp, http.StatusOK)

			resp = e.get(t, "/api/v1/settings/jobs-queue", e.adminToken(t))
			assertStatus(t, resp, http.StatusOK)
			var data queueSettings
			decodeData(t, resp, &data)
			if data.TimeoutMinutes != v {
				t.Errorf("timeout_minutes = %d, want %d", data.TimeoutMinutes, v)
			}
		}
	})

	t.Run("returns 400 out of range", func(t *testing.T) {
		e := newTestEnv(t)
		for _, v := range []int{-1, 30*24*60 + 1} {
			resp := e.doJSON(t, "PUT", "/api/v1/settings/jobs-queue", e.adminToken(t), map[string]any{"timeout_minutes": v})
			assertStatus(t, resp, http.StatusBadRequest)
		}
	})

	t.Run("returns 403 for non-admin", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/settings/jobs-queue", e.userToken(t))
		assertStatus(t, resp, http.StatusForbidden)
	})
}

// TestSettingsHandler_OIDCAccessFields covers the group restrictions and
// session cap of an OIDC provider (issue #277): normalisation of the group
// lists, the groups claim required by any group, the session cap bounds and
// its default, and an update that omits the cap keeping the stored one.
// The subtests share one server, since a test server is slow to start.
func TestSettingsHandler_OIDCAccessFields(t *testing.T) {
	e := newTestEnv(t)
	base := func(extra map[string]any) map[string]any {
		body := map[string]any{
			"name":          "IdP",
			"issuer":        "https://idp.example.com",
			"client_id":     "cid",
			"client_secret": "sec",
		}
		for k, v := range extra {
			body[k] = v
		}
		return body
	}
	type accessData struct {
		ID              string `json:"id"`
		GroupsClaim     string `json:"groups_claim"`
		AllowedGroups   string `json:"allowed_groups"`
		AdminGroups     string `json:"admin_groups"`
		SessionMaxHours int    `json:"session_max_hours"`
	}

	t.Run("create normalises the fields and defaults the session cap", func(t *testing.T) {
		resp := e.post(t, "/api/v1/settings/oidc", e.adminToken(t), base(map[string]any{
			"groups_claim":   " groups ",
			"allowed_groups": " arkeep-users ,, Arkeep Ops ",
			"admin_groups":   "arkeep-admins,",
		}))
		assertStatus(t, resp, http.StatusCreated)
		var got accessData
		decodeData(t, resp, &got)
		want := accessData{ID: got.ID, GroupsClaim: "groups", AllowedGroups: "arkeep-users, Arkeep Ops", AdminGroups: "arkeep-admins", SessionMaxHours: 12}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	invalid := []struct {
		name  string
		extra map[string]any
	}{
		{"allowed groups without groups claim", map[string]any{"allowed_groups": "arkeep-users"}},
		{"admin groups without groups claim", map[string]any{"groups_claim": "  ", "admin_groups": "arkeep-admins"}},
		{"session cap below 1", map[string]any{"session_max_hours": 0}},
		{"session cap above the refresh token lifetime", map[string]any{"session_max_hours": 169}},
	}
	for _, tt := range invalid {
		t.Run("create rejects "+tt.name, func(t *testing.T) {
			resp := e.post(t, "/api/v1/settings/oidc", e.adminToken(t), base(tt.extra))
			assertStatus(t, resp, http.StatusBadRequest)
		})
	}

	t.Run("blank group entries need no groups claim", func(t *testing.T) {
		resp := e.post(t, "/api/v1/settings/oidc", e.adminToken(t), base(map[string]any{"allowed_groups": " , "}))
		assertStatus(t, resp, http.StatusCreated)
	})

	t.Run("update sets the fields and an omitted cap keeps the stored one", func(t *testing.T) {
		createResp := e.post(t, "/api/v1/settings/oidc", e.adminToken(t), base(map[string]any{"session_max_hours": 8}))
		assertStatus(t, createResp, http.StatusCreated)
		var created accessData
		decodeData(t, createResp, &created)

		update := base(map[string]any{"groups_claim": "roles", "admin_groups": "admins"})
		delete(update, "client_secret")
		resp := e.doJSON(t, "PUT", "/api/v1/settings/oidc/"+created.ID, e.adminToken(t), update)
		assertStatus(t, resp, http.StatusOK)
		var got accessData
		decodeData(t, resp, &got)
		want := accessData{ID: created.ID, GroupsClaim: "roles", AdminGroups: "admins", SessionMaxHours: 8}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}

		update["session_max_hours"] = 200
		resp = e.doJSON(t, "PUT", "/api/v1/settings/oidc/"+created.ID, e.adminToken(t), update)
		assertStatus(t, resp, http.StatusBadRequest)

		// The audit entry records who can sign in and for how long.
		entries, _, err := e.deps.audit.List(context.Background(),
			repositories.AuditFilter{Action: "settings.oidc.update"}, repositories.ListOptions{Limit: 10})
		if err != nil || len(entries) != 1 {
			t.Fatalf("audit List = %d entries, %v; want 1", len(entries), err)
		}
		var details map[string]any
		if err := json.Unmarshal([]byte(entries[0].Details), &details); err != nil {
			t.Fatalf("audit details: %v", err)
		}
		if details["groups_claim"] != "roles" || details["admin_groups"] != "admins" ||
			details["allowed_groups"] != "" || details["session_max_hours"] != float64(8) {
			t.Errorf("audit details = %v, want the group settings and session cap", details)
		}
	})
}
