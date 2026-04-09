package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/arkeep-io/arkeep/server/internal/scheduler"
)

// healthHandler serves the /health/live and /health/ready endpoints.
//
// /health/live  — liveness probe: the process is up and responding.
// /health/ready — readiness probe: the process can serve traffic
//                 (database reachable, scheduler running).
type healthHandler struct {
	db        *sql.DB
	scheduler *scheduler.Scheduler
}

func newHealthHandler(db *sql.DB, sched *scheduler.Scheduler) *healthHandler {
	return &healthHandler{db: db, scheduler: sched}
}

// healthCheckResult is the per-check entry in the /health/ready response body.
type healthCheckResult struct {
	Status    string `json:"status"`              // "ok" | "error"
	LatencyMs *int64 `json:"latency_ms,omitempty"` // only for database
	Error     string `json:"error,omitempty"`
}

// healthResponse is the full /health/ready response body.
type healthResponse struct {
	Status string                       `json:"status"` // "healthy" | "unhealthy"
	Checks map[string]healthCheckResult `json:"checks"`
}

// Live handles GET /health/live.
// Always returns 200 OK — if the process can respond, it is alive.
func (h *healthHandler) Live(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok")) //nolint:errcheck
}

// Ready handles GET /health/ready.
// Runs lightweight dependency checks and returns:
//   - 200 + status "healthy"   when all checks pass
//   - 503 + status "unhealthy" when any check fails
//
// Example response:
//
//	{
//	  "status": "healthy",
//	  "checks": {
//	    "database":  { "status": "ok", "latency_ms": 2 },
//	    "scheduler": { "status": "ok" }
//	  }
//	}
func (h *healthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	dbResult := h.checkDatabase(r.Context())
	schedResult := h.checkScheduler()

	allOK := dbResult.Status == "ok" && schedResult.Status == "ok"

	status := "healthy"
	httpStatus := http.StatusOK
	if !allOK {
		status = "unhealthy"
		httpStatus = http.StatusServiceUnavailable
	}

	resp := healthResponse{
		Status: status,
		Checks: map[string]healthCheckResult{
			"database":  dbResult,
			"scheduler": schedResult,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// checkDatabase pings the database with a 2-second timeout and reports the
// round-trip latency. Returns status "error" on timeout or connection failure.
func (h *healthHandler) checkDatabase(ctx context.Context) healthCheckResult {
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	start := time.Now()
	if err := h.db.PingContext(pingCtx); err != nil {
		return healthCheckResult{Status: "error", Error: err.Error()}
	}
	ms := time.Since(start).Milliseconds()
	return healthCheckResult{Status: "ok", LatencyMs: &ms}
}

// checkScheduler verifies that the gocron scheduler is currently running.
func (h *healthHandler) checkScheduler() healthCheckResult {
	if !h.scheduler.IsRunning() {
		return healthCheckResult{Status: "error", Error: "scheduler is not running"}
	}
	return healthCheckResult{Status: "ok"}
}
