package api

import (
	"net/http"
	"testing"
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
