package api

import (
	"net/http"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// DashboardHandler serves the aggregated statistics endpoint used by the
// dashboard page. It delegates all computation to DashboardRepository so
// the handler itself remains a thin HTTP adapter with no business logic.
type DashboardHandler struct {
	repo   repositories.DashboardRepository
	logger *zap.Logger
}

// NewDashboardHandler creates a new DashboardHandler.
func NewDashboardHandler(repo repositories.DashboardRepository, logger *zap.Logger) *DashboardHandler {
	return &DashboardHandler{
		repo:   repo,
		logger: logger.Named("dashboard_handler"),
	}
}

// -----------------------------------------------------------------------------
// Response types
// -----------------------------------------------------------------------------

// dayJobActivityResponse is the per-day job counts for the activity chart.
type dayJobActivityResponse struct {
	Date      string `json:"date"`       // "YYYY-MM-DD"
	Succeeded int64  `json:"succeeded"`
	Failed    int64  `json:"failed"`
}

// daySizeActivityResponse is the per-day backed-up size for the size chart.
type daySizeActivityResponse struct {
	Date      string `json:"date"`       // "YYYY-MM-DD"
	SizeBytes int64  `json:"size_bytes"`
}

// dashboardResponse is the full payload returned by GET /api/v1/dashboard.
// All fields are computed server-side so the frontend never needs to paginate
// through large lists to derive aggregate values.
type dashboardResponse struct {
	// Agents
	AgentsTotal  int64 `json:"agents_total"`
	AgentsOnline int64 `json:"agents_online"`

	// Policies
	PoliciesTotal  int64 `json:"policies_total"`
	PoliciesActive int64 `json:"policies_active"`

	// Jobs today (UTC calendar day)
	JobsTodayTotal     int64 `json:"jobs_today_total"`
	JobsTodaySucceeded int64 `json:"jobs_today_succeeded"`
	JobsTodayFailed    int64 `json:"jobs_today_failed"`

	// Snapshots (all time)
	SnapshotsTotal     int64 `json:"snapshots_total"`
	SnapshotsTotalSize int64 `json:"snapshots_total_size"` // bytes

	// 7-day activity arrays (index 0 = 6 days ago, index 6 = today)
	JobActivity  []dayJobActivityResponse  `json:"job_activity"`
	SizeActivity []daySizeActivityResponse `json:"size_activity"`
}

// -----------------------------------------------------------------------------
// Handlers
// -----------------------------------------------------------------------------

// Get handles GET /api/v1/dashboard.
// Returns all aggregated statistics in a single response so the dashboard
// page requires only one round-trip.
func (h *DashboardHandler) Get(w http.ResponseWriter, r *http.Request) {
	stats, err := h.repo.GetStats(r.Context())
	if err != nil {
		h.logger.Error("failed to compute dashboard stats", zap.Error(err))
		ErrInternal(w)
		return
	}

	jobActivity := make([]dayJobActivityResponse, len(stats.JobActivity))
	for i, d := range stats.JobActivity {
		jobActivity[i] = dayJobActivityResponse{
			Date:      d.Date,
			Succeeded: d.Succeeded,
			Failed:    d.Failed,
		}
	}

	sizeActivity := make([]daySizeActivityResponse, len(stats.SizeActivity))
	for i, d := range stats.SizeActivity {
		sizeActivity[i] = daySizeActivityResponse{
			Date:      d.Date,
			SizeBytes: d.SizeBytes,
		}
	}

	Ok(w, dashboardResponse{
		AgentsTotal:        stats.AgentsTotal,
		AgentsOnline:       stats.AgentsOnline,
		PoliciesTotal:      stats.PoliciesTotal,
		PoliciesActive:     stats.PoliciesActive,
		JobsTodayTotal:     stats.JobsTodayTotal,
		JobsTodaySucceeded: stats.JobsTodaySucceeded,
		JobsTodayFailed:    stats.JobsTodayFailed,
		SnapshotsTotal:     stats.SnapshotsTotal,
		SnapshotsTotalSize: stats.SnapshotsTotalSize,
		JobActivity:        jobActivity,
		SizeActivity:       sizeActivity,
	})
}