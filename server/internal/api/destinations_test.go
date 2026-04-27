package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// createDBDestination inserts a destination record directly.
func createDBDestination(t *testing.T, deps *testDeps, name, destType string) *db.Destination {
	t.Helper()
	d := &db.Destination{
		Name:        name,
		Type:        destType,
		Credentials: db.EncryptedString(`{"bucket":"test"}`),
		Config:      `{}`,
		Enabled:     true,
	}
	if err := deps.dests.Create(context.Background(), d); err != nil {
		t.Fatalf("createDBDestination: %v", err)
	}
	return d
}

func TestDestinationHandler_List(t *testing.T) {
	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/destinations", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("returns empty list on fresh DB", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/destinations", e.adminToken(t))
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

	t.Run("returns created destinations", func(t *testing.T) {
		e := newTestEnv(t)
		createDBDestination(t, e.deps, "s3-backup", "s3")
		createDBDestination(t, e.deps, "local-backup", "local")

		resp := e.get(t, "/api/v1/destinations", e.adminToken(t))
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

func TestDestinationHandler_Create(t *testing.T) {
	t.Run("creates destination and returns 201", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/destinations", e.adminToken(t), map[string]string{
			"name":        "my-s3-bucket",
			"type":        "s3",
			"credentials": `{"access_key":"AKIA...","secret_key":"..."}`,
			"config":      `{"bucket":"backups","region":"us-east-1"}`,
		})
		assertStatus(t, resp, http.StatusCreated)

		var data struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Type    string `json:"type"`
			Enabled bool   `json:"enabled"`
		}
		decodeData(t, resp, &data)
		if data.Name != "my-s3-bucket" {
			t.Errorf("name = %q, want my-s3-bucket", data.Name)
		}
		if data.Type != "s3" {
			t.Errorf("type = %q, want s3", data.Type)
		}
		if !data.Enabled {
			t.Error("enabled = false, want true")
		}
	})

	t.Run("returns 400 when name is missing", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/destinations", e.adminToken(t), map[string]string{
			"type": "s3",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 for invalid type", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/destinations", e.adminToken(t), map[string]string{
			"name": "dest",
			"type": "dropbox", // not in validDestinationTypes
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("accepts all valid destination types", func(t *testing.T) {
		for _, typ := range []string{"local", "s3", "sftp", "rest", "rclone"} {
			e := newTestEnv(t)
			resp := e.post(t, "/api/v1/destinations", e.adminToken(t), map[string]string{
				"name": "dest-" + typ,
				"type": typ,
			})
			assertStatus(t, resp, http.StatusCreated)
		}
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/destinations", "", map[string]string{
			"name": "dest",
			"type": "s3",
		})
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestDestinationHandler_GetByID(t *testing.T) {
	t.Run("returns destination by UUID", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "sftp-target", "sftp")

		resp := e.get(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		}
		decodeData(t, resp, &data)
		if data.ID != dest.ID.String() {
			t.Errorf("id = %q, want %q", data.ID, dest.ID.String())
		}
		if data.Type != "sftp" {
			t.Errorf("type = %q, want sftp", data.Type)
		}
	})

	t.Run("returns 404 for non-existent destination", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/destinations/00000000-0000-0000-0000-000000000001", e.adminToken(t))
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 400 for malformed UUID", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/destinations/not-a-uuid", e.adminToken(t))
		assertStatus(t, resp, http.StatusBadRequest)
	})
}

func TestDestinationHandler_Update(t *testing.T) {
	t.Run("updates destination name", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "old-name", "s3")

		name := "new-name"
		resp := e.patch(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t), map[string]any{
			"name": &name,
		})
		assertStatus(t, resp, http.StatusOK)

		var data struct{ Name string `json:"name"` }
		decodeData(t, resp, &data)
		if data.Name != "new-name" {
			t.Errorf("name = %q, want new-name", data.Name)
		}
	})

	t.Run("returns 404 for non-existent destination", func(t *testing.T) {
		e := newTestEnv(t)
		name := "x"
		resp := e.patch(t, "/api/v1/destinations/00000000-0000-0000-0000-000000000001", e.adminToken(t), map[string]any{
			"name": &name,
		})
		assertStatus(t, resp, http.StatusNotFound)
	})
}

func TestDestinationHandler_Delete(t *testing.T) {
	t.Run("deletes destination successfully", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "to-delete", "local")

		resp := e.del(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusNoContent)
	})

	t.Run("returns 404 for non-existent destination", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.del(t, "/api/v1/destinations/00000000-0000-0000-0000-000000000001", e.adminToken(t))
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.del(t, "/api/v1/destinations/00000000-0000-0000-0000-000000000001", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}
