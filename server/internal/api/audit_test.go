package api

import (
	"net/http"
	"testing"
)

func TestAuditHandler_List(t *testing.T) {
	t.Run("admin can list audit log", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/audit", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		}
		decodeData(t, resp, &data)
		if data.Total != 0 {
			t.Errorf("total = %d, want 0 (fresh DB)", data.Total)
		}
	})

	t.Run("audit log reflects admin actions", func(t *testing.T) {
		e := newTestEnv(t)
		// Creating a destination generates an audit record.
		e.post(t, "/api/v1/destinations", e.adminToken(t), map[string]string{
			"name": "s3-for-audit",
			"type": "s3",
		})

		resp := e.get(t, "/api/v1/audit", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		}
		decodeData(t, resp, &data)
		if data.Total == 0 {
			t.Error("total = 0, want > 0 after creating a destination")
		}
	})

	t.Run("returns 403 for non-admin", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/audit", e.userToken(t))
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/audit", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestVersionHandler_Get(t *testing.T) {
	t.Run("returns server version for authenticated user", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/version", e.userToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			ServerVersion   string `json:"server_version"`
			LatestVersion   string `json:"latest_version"`
			UpdateAvailable bool   `json:"update_available"`
		}
		decodeData(t, resp, &data)
		if data.ServerVersion != "0.0.0-test" {
			t.Errorf("server_version = %q, want 0.0.0-test", data.ServerVersion)
		}
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/version", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}
