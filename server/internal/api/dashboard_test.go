package api

import (
	"net/http"
	"testing"
)

func TestDashboardHandler_Get(t *testing.T) {
	t.Run("returns 200 with dashboard stats for admin", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/dashboard", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			AgentsTotal    int64 `json:"agents_total"`
			PoliciesTotal  int64 `json:"policies_total"`
			SnapshotsTotal int64 `json:"snapshots_total"`
			JobActivity    []any `json:"job_activity"`
			SizeActivity   []any `json:"size_activity"`
		}
		decodeData(t, resp, &data)
		// All counters start at zero on a fresh DB.
		if data.AgentsTotal != 0 {
			t.Errorf("agents_total = %d, want 0", data.AgentsTotal)
		}
		// Activity arrays must be present (7-day window).
		if data.JobActivity == nil {
			t.Error("job_activity is nil, want non-nil array")
		}
	})

	t.Run("returns 200 for regular user (not admin-only)", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/dashboard", e.userToken(t))
		assertStatus(t, resp, http.StatusOK)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/dashboard", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("reflects created agents in total count", func(t *testing.T) {
		e := newTestEnv(t)
		createDBAgent(t, e.deps, "agent-1")
		createDBAgent(t, e.deps, "agent-2")

		resp := e.get(t, "/api/v1/dashboard", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			AgentsTotal int64 `json:"agents_total"`
		}
		decodeData(t, resp, &data)
		if data.AgentsTotal != 2 {
			t.Errorf("agents_total = %d, want 2", data.AgentsTotal)
		}
	})
}
