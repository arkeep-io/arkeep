package api

import (
	"net/http"
	"testing"
)

func TestUserHandler_GetMe(t *testing.T) {
	t.Run("returns own profile for authenticated user", func(t *testing.T) {
		e := newTestEnv(t)
		userID := createDBUser(t, e.deps, "me@example.com", "admin")

		// Issue a token whose UserID matches the DB user so GetMe can look them up.
		token := e.tokenForUser(t, userID, "admin")

		resp := e.get(t, "/api/v1/users/me", token)
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			ID    string `json:"id"`
			Email string `json:"email"`
			Role  string `json:"role"`
		}
		decodeData(t, resp, &data)
		if data.ID != userID.String() {
			t.Errorf("id = %q, want %q", data.ID, userID.String())
		}
		if data.Email != "me@example.com" {
			t.Errorf("email = %q, want me@example.com", data.Email)
		}
		if data.Role != "admin" {
			t.Errorf("role = %q, want admin", data.Role)
		}
	})

	t.Run("returns 404 when user in token does not exist in DB", func(t *testing.T) {
		e := newTestEnv(t)
		// Token references a UUID that was never inserted.
		token := e.adminToken(t) // random UUID, not in DB

		resp := e.get(t, "/api/v1/users/me", token)
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/users/me", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestUserHandler_List(t *testing.T) {
	t.Run("admin can list users", func(t *testing.T) {
		e := newTestEnv(t)
		createDBUser(t, e.deps, "alice@example.com", "admin")
		createDBUser(t, e.deps, "bob@example.com", "user")

		resp := e.get(t, "/api/v1/users", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []struct{ Email string `json:"email"` } `json:"items"`
			Total int64                                   `json:"total"`
		}
		decodeData(t, resp, &data)
		if data.Total != 2 {
			t.Errorf("total = %d, want 2", data.Total)
		}
	})

	t.Run("returns 403 for non-admin user", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/users", e.userToken(t))
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/users", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestUserHandler_Create(t *testing.T) {
	// Note: each Create call hashes a password with Argon2 (~100 ms).
	validUser := map[string]string{
		"email":        "newuser@example.com",
		"password":     "StrongPassword123!",
		"display_name": "New User",
		"role":         "user",
	}

	t.Run("admin creates a user successfully", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/users", e.adminToken(t), validUser)
		assertStatus(t, resp, http.StatusCreated)

		var data struct {
			Email       string `json:"email"`
			DisplayName string `json:"display_name"`
			Role        string `json:"role"`
			IsActive    bool   `json:"is_active"`
		}
		decodeData(t, resp, &data)
		if data.Email != "newuser@example.com" {
			t.Errorf("email = %q, want newuser@example.com", data.Email)
		}
		if data.Role != "user" {
			t.Errorf("role = %q, want user", data.Role)
		}
		if !data.IsActive {
			t.Error("is_active = false, want true")
		}
	})

	t.Run("returns 409 for duplicate email", func(t *testing.T) {
		e := newTestEnv(t)
		createDBUser(t, e.deps, "existing@example.com", "user")

		resp := e.post(t, "/api/v1/users", e.adminToken(t), map[string]string{
			"email":        "existing@example.com",
			"password":     "password123",
			"display_name": "Duplicate",
			"role":         "user",
		})
		assertStatus(t, resp, http.StatusConflict)
	})

	t.Run("returns 400 when email is missing", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/users", e.adminToken(t), map[string]string{
			"password":     "password123",
			"display_name": "User",
			"role":         "user",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when role is invalid", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/users", e.adminToken(t), map[string]string{
			"email":        "user@example.com",
			"password":     "password123",
			"display_name": "User",
			"role":         "superuser",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 403 for non-admin user", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/users", e.userToken(t), validUser)
		assertStatus(t, resp, http.StatusForbidden)
	})
}

func TestUserHandler_GetByID(t *testing.T) {
	t.Run("admin can get user by ID", func(t *testing.T) {
		e := newTestEnv(t)
		userID := createDBUser(t, e.deps, "target@example.com", "user")

		resp := e.get(t, "/api/v1/users/"+userID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		}
		decodeData(t, resp, &data)
		if data.ID != userID.String() {
			t.Errorf("id = %q, want %q", data.ID, userID.String())
		}
	})

	t.Run("returns 404 for non-existent user", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/users/00000000-0000-0000-0000-000000000001", e.adminToken(t))
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 403 for non-admin user", func(t *testing.T) {
		e := newTestEnv(t)
		userID := createDBUser(t, e.deps, "target@example.com", "user")

		resp := e.get(t, "/api/v1/users/"+userID.String(), e.userToken(t))
		assertStatus(t, resp, http.StatusForbidden)
	})
}

func TestUserHandler_Delete(t *testing.T) {
	t.Run("admin can delete a user", func(t *testing.T) {
		e := newTestEnv(t)
		userID := createDBUser(t, e.deps, "todelete@example.com", "user")

		resp := e.del(t, "/api/v1/users/"+userID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusNoContent)
	})

	t.Run("returns 403 for non-admin", func(t *testing.T) {
		e := newTestEnv(t)
		userID := createDBUser(t, e.deps, "protected@example.com", "user")

		resp := e.del(t, "/api/v1/users/"+userID.String(), e.userToken(t))
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("returns 404 for non-existent user", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.del(t, "/api/v1/users/00000000-0000-0000-0000-000000000001", e.adminToken(t))
		assertStatus(t, resp, http.StatusNotFound)
	})
}

func TestUserHandler_Update(t *testing.T) {
	t.Run("admin updates display name", func(t *testing.T) {
		e := newTestEnv(t)
		userID := createDBUser(t, e.deps, "update-me@example.com", "user")

		name := "Updated Name"
		resp := e.patch(t, "/api/v1/users/"+userID.String(), e.adminToken(t), map[string]any{
			"display_name": &name,
		})
		assertStatus(t, resp, http.StatusOK)

		var data struct{ DisplayName string `json:"display_name"` }
		decodeData(t, resp, &data)
		if data.DisplayName != "Updated Name" {
			t.Errorf("display_name = %q, want Updated Name", data.DisplayName)
		}
	})

	t.Run("returns 404 for non-existent user", func(t *testing.T) {
		e := newTestEnv(t)
		name := "x"
		resp := e.patch(t, "/api/v1/users/00000000-0000-0000-0000-000000000001", e.adminToken(t), map[string]any{
			"display_name": &name,
		})
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 400 for empty display_name", func(t *testing.T) {
		e := newTestEnv(t)
		userID := createDBUser(t, e.deps, "update2@example.com", "user")

		empty := ""
		resp := e.patch(t, "/api/v1/users/"+userID.String(), e.adminToken(t), map[string]any{
			"display_name": &empty,
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 403 for non-admin", func(t *testing.T) {
		e := newTestEnv(t)
		userID := createDBUser(t, e.deps, "update3@example.com", "user")
		name := "Name"
		resp := e.patch(t, "/api/v1/users/"+userID.String(), e.userToken(t), map[string]any{
			"display_name": &name,
		})
		assertStatus(t, resp, http.StatusForbidden)
	})
}

func TestUserHandler_UpdateMe(t *testing.T) {
	t.Run("user updates own display name", func(t *testing.T) {
		e := newTestEnv(t)
		userID := createDBUser(t, e.deps, "me-update@example.com", "user")
		token := e.tokenForUser(t, userID, "user")

		name := "My New Name"
		resp := e.patch(t, "/api/v1/users/me", token, map[string]any{
			"display_name": &name,
		})
		assertStatus(t, resp, http.StatusOK)

		var data struct{ DisplayName string `json:"display_name"` }
		decodeData(t, resp, &data)
		if data.DisplayName != "My New Name" {
			t.Errorf("display_name = %q, want My New Name", data.DisplayName)
		}
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		name := "x"
		resp := e.patch(t, "/api/v1/users/me", "", map[string]any{
			"display_name": &name,
		})
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}
