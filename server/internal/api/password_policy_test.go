package api

import (
	"net/http"
	"testing"
)

// TestPasswordPolicy_EveryPath guards against the password policy differing by
// path: the minimum length used to be enforced only by the reset flow.
func TestPasswordPolicy_EveryPath(t *testing.T) {
	const short = "short"

	t.Run("setup", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/setup/complete", "", map[string]string{
			"name": "Admin", "email": "admin@example.com", "password": short,
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("admin creates a user", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/users", e.adminToken(t), map[string]string{
			"email": "new@example.com", "display_name": "New", "role": "user", "password": short,
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("admin updates a user", func(t *testing.T) {
		e := newTestEnv(t)
		userID := createDBUser(t, e.deps, "update-pw@example.com", "user")
		resp := e.patch(t, "/api/v1/users/"+userID.String(), e.adminToken(t), map[string]any{
			"password": short,
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("user changes own password", func(t *testing.T) {
		e := newTestEnv(t)
		userID := createDBUser(t, e.deps, "me-pw@example.com", "user")
		resp := e.patch(t, "/api/v1/users/me", e.tokenForUser(t, userID, "user"), map[string]any{
			"password": short,
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})
}
