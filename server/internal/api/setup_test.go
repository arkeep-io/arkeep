package api

import (
	"net/http"
	"testing"
)

func TestSetupHandler_GetStatus(t *testing.T) {
	t.Run("returns completed=false on empty database", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/setup/status", "")
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Completed bool `json:"completed"`
		}
		decodeData(t, resp, &data)
		if data.Completed {
			t.Error("completed = true, want false on empty DB")
		}
	})

	t.Run("returns completed=true after a user exists", func(t *testing.T) {
		e := newTestEnv(t)
		createDBUser(t, e.deps, "admin@test.local", "admin")

		resp := e.get(t, "/api/v1/setup/status", "")
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Completed bool `json:"completed"`
		}
		decodeData(t, resp, &data)
		if !data.Completed {
			t.Error("completed = false, want true after user created")
		}
	})
}

func TestSetupHandler_Complete(t *testing.T) {
	t.Run("creates admin user and returns 201", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/setup/complete", "", map[string]string{
			"name":     "Admin User",
			"email":    "admin@example.com",
			"password": "secure-password-123",
		})
		assertStatus(t, resp, http.StatusCreated)
	})

	t.Run("returns 409 if setup already completed", func(t *testing.T) {
		e := newTestEnv(t)
		createDBUser(t, e.deps, "existing@example.com", "admin")

		resp := e.post(t, "/api/v1/setup/complete", "", map[string]string{
			"name":     "Second Admin",
			"email":    "second@example.com",
			"password": "password123",
		})
		assertStatus(t, resp, http.StatusConflict)
	})

	t.Run("returns 400 when name is missing", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/setup/complete", "", map[string]string{
			"email":    "admin@example.com",
			"password": "password123",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when email is missing", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/setup/complete", "", map[string]string{
			"name":     "Admin",
			"password": "password123",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when password is missing", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/setup/complete", "", map[string]string{
			"name":  "Admin",
			"email": "admin@example.com",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 on unknown fields", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/setup/complete", "", map[string]string{
			"name":     "Admin",
			"email":    "admin@example.com",
			"password": "password123",
			"unknown":  "field",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})
}
