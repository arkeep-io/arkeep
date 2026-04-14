package api

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestHealthHandler_Live(t *testing.T) {
	t.Run("returns 200 OK", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/health/live", "")
		assertStatus(t, resp, http.StatusOK)
	})

	t.Run("does not require authentication", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/health/live", "")
		if resp.StatusCode == http.StatusUnauthorized {
			t.Error("health/live must not require authentication")
		}
	})
}

func TestHealthHandler_Ready(t *testing.T) {
	t.Run("returns JSON with checks map", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/health/ready", "")

		// /health/ready returns 503 in tests because the scheduler is not
		// started (deterministic for tests), but the shape must be correct.
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var payload healthResponse
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("ready response is not valid JSON: %v — body: %s", err, body)
		}
		if payload.Status == "" {
			t.Error("status field is empty")
		}
		if _, ok := payload.Checks["database"]; !ok {
			t.Error("checks map missing 'database' key")
		}
		if _, ok := payload.Checks["scheduler"]; !ok {
			t.Error("checks map missing 'scheduler' key")
		}
	})

	t.Run("reports database check ok on healthy DB", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/health/ready", "")

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var payload healthResponse
		json.Unmarshal(body, &payload) //nolint:errcheck
		if payload.Checks["database"].Status != "ok" {
			t.Errorf("database check = %q, want ok", payload.Checks["database"].Status)
		}
	})

	t.Run("returns 503 when scheduler is not running", func(t *testing.T) {
		// In tests the scheduler is never started, so IsRunning() is false.
		e := newTestEnv(t)
		resp := e.get(t, "/health/ready", "")
		assertStatus(t, resp, http.StatusServiceUnavailable)
	})

	t.Run("does not require authentication", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/health/ready", "")
		if resp.StatusCode == http.StatusUnauthorized {
			t.Error("health/ready must not require authentication")
		}
	})
}
