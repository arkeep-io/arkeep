package api

import (
	"net/http"
	"testing"
)

// TestRouter_UserRoleIsReadOnly pins every route that changes backup
// configuration or makes an agent act as admin-only. Objects have no owner, so
// a write open to the "user" role is a write on every agent: a user could back
// up any host to a repository they control, redirect an existing destination,
// or (through destination env vars) run commands on agents.
func TestRouter_UserRoleIsReadOnly(t *testing.T) {
	const id = "00000000-0000-0000-0000-000000000001"
	routes := []struct {
		method, path string
	}{
		{http.MethodPost, "/api/v1/agents"},
		{http.MethodPatch, "/api/v1/agents/" + id},
		{http.MethodGet, "/api/v1/agents/" + id + "/volumes"},
		{http.MethodPost, "/api/v1/destinations"},
		{http.MethodPatch, "/api/v1/destinations/" + id},
		{http.MethodPost, "/api/v1/destinations/" + id + "/import"},
		{http.MethodPost, "/api/v1/destinations/" + id + "/check-repo"},
		{http.MethodPost, "/api/v1/policies"},
		{http.MethodPatch, "/api/v1/policies/" + id},
		{http.MethodPost, "/api/v1/jobs/" + id + "/cancel"},
		{http.MethodGet, "/api/v1/snapshots/" + id + "/browse"},
	}

	e := newTestEnv(t)
	token := e.userToken(t)
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			resp := e.doJSON(t, rt.method, rt.path, token, map[string]any{})
			_ = resp.Body.Close()
			assertStatus(t, resp, http.StatusForbidden)
		})
	}

	// Read routes stay open to the user role.
	for _, path := range []string{"/api/v1/agents", "/api/v1/destinations", "/api/v1/policies", "/api/v1/jobs", "/api/v1/snapshots"} {
		t.Run("GET "+path, func(t *testing.T) {
			resp := e.get(t, path, token)
			_ = resp.Body.Close()
			assertStatus(t, resp, http.StatusOK)
		})
	}
}
