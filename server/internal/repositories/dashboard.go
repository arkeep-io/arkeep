package repositories

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// DashboardStats holds all aggregated data needed by the dashboard endpoint.
// It is computed in a single repository call that executes several lightweight
// SQL queries in sequence — fast because all are index-backed COUNT/SUM queries.
type DashboardStats struct {
	// Agent counts
	AgentsTotal  int64
	AgentsOnline int64

	// Policy counts
	PoliciesTotal  int64
	PoliciesActive int64

	// Job counts for today (UTC)
	JobsTodayTotal     int64
	JobsTodaySucceeded int64
	JobsTodayFailed    int64

	// Snapshot totals (all time)
	SnapshotsTotal     int64
	SnapshotsTotalSize int64 // sum of size_bytes

	// Activity over the last 7 days (index 0 = oldest, index 6 = today)
	JobActivity  []DayJobActivity
	SizeActivity []DaySizeActivity
}

// DayJobActivity holds the succeeded and failed job counts for a single calendar day.
type DayJobActivity struct {
	Date      string // "YYYY-MM-DD"
	Succeeded int64
	Failed    int64
}

// DaySizeActivity holds the total bytes backed up for a single calendar day,
// derived from snapshot records created on that day.
type DaySizeActivity struct {
	Date      string // "YYYY-MM-DD"
	SizeBytes int64
}

// DashboardRepository computes aggregated statistics for the dashboard.
// All queries are read-only and do not modify any data.
type DashboardRepository interface {
	GetStats(ctx context.Context) (*DashboardStats, error)
}

// gormDashboardRepository is the GORM implementation of DashboardRepository.
type gormDashboardRepository struct {
	db *gorm.DB
}

// NewDashboardRepository returns a DashboardRepository backed by the provided *gorm.DB.
func NewDashboardRepository(db *gorm.DB) DashboardRepository {
	return &gormDashboardRepository{db: db}
}

// GetStats executes all dashboard aggregation queries and returns the results
// as a single DashboardStats struct. Each query is a simple COUNT or SUM on
// an indexed column — none require full table scans.
func (r *gormDashboardRepository) GetStats(ctx context.Context) (*DashboardStats, error) {
	stats := &DashboardStats{}
	d := r.db.WithContext(ctx)

	// ── Agents ───────────────────────────────────────────────────────────────

	if err := d.Raw(`SELECT COUNT(*) FROM agents WHERE deleted_at IS NULL`).
		Scan(&stats.AgentsTotal).Error; err != nil {
		return nil, fmt.Errorf("dashboard: agents total: %w", err)
	}

	if err := d.Raw(`SELECT COUNT(*) FROM agents WHERE deleted_at IS NULL AND status = 'online'`).
		Scan(&stats.AgentsOnline).Error; err != nil {
		return nil, fmt.Errorf("dashboard: agents online: %w", err)
	}

	// ── Policies ─────────────────────────────────────────────────────────────

	if err := d.Raw(`SELECT COUNT(*) FROM policies WHERE deleted_at IS NULL`).
		Scan(&stats.PoliciesTotal).Error; err != nil {
		return nil, fmt.Errorf("dashboard: policies total: %w", err)
	}

	if err := d.Raw(`SELECT COUNT(*) FROM policies WHERE deleted_at IS NULL AND enabled = true`).
		Scan(&stats.PoliciesActive).Error; err != nil {
		return nil, fmt.Errorf("dashboard: policies active: %w", err)
	}

	// ── Jobs today ───────────────────────────────────────────────────────────
	// "Today" is defined as the current UTC calendar day (midnight to midnight).
	// Using DATE() is portable across SQLite and PostgreSQL.

	today := time.Now().UTC().Format("2006-01-02")

	// substr(col, 1, 10) extracts the "YYYY-MM-DD" prefix from the stored
	// timestamp string. SQLite's DATE() function only handles up to 3 fractional
	// second digits, but GORM stores time.Time as RFC3339Nano (9 digits), so
	// DATE() returns NULL for every row — causing all counts to be 0.
	// substr is format-agnostic and works for any ISO 8601 timestamp.

	if err := d.Raw(`SELECT COUNT(*) FROM jobs WHERE substr(created_at, 1, 10) = ?`, today).
		Scan(&stats.JobsTodayTotal).Error; err != nil {
		return nil, fmt.Errorf("dashboard: jobs today total: %w", err)
	}

	if err := d.Raw(`SELECT COUNT(*) FROM jobs WHERE substr(created_at, 1, 10) = ? AND status = 'succeeded'`, today).
		Scan(&stats.JobsTodaySucceeded).Error; err != nil {
		return nil, fmt.Errorf("dashboard: jobs today succeeded: %w", err)
	}

	if err := d.Raw(`SELECT COUNT(*) FROM jobs WHERE substr(created_at, 1, 10) = ? AND status = 'failed'`, today).
		Scan(&stats.JobsTodayFailed).Error; err != nil {
		return nil, fmt.Errorf("dashboard: jobs today failed: %w", err)
	}

	// ── Snapshots ────────────────────────────────────────────────────────────

	if err := d.Raw(`SELECT COUNT(*) FROM snapshots`).
		Scan(&stats.SnapshotsTotal).Error; err != nil {
		return nil, fmt.Errorf("dashboard: snapshots total: %w", err)
	}

	if err := d.Raw(`SELECT COALESCE(SUM(size_bytes), 0) FROM snapshots`).
		Scan(&stats.SnapshotsTotalSize).Error; err != nil {
		return nil, fmt.Errorf("dashboard: snapshots size: %w", err)
	}

	// ── Job activity — last 7 days ────────────────────────────────────────────
	// Returns one row per (date, status) combination. Days with no jobs are
	// absent from the result and filled with zeros in the handler.

	type jobActivityRow struct {
		Date   string
		Status string
		Count  int64
	}

	weekStart := time.Now().UTC().AddDate(0, 0, -6).Format("2006-01-02")

	var jobRows []jobActivityRow
	if err := d.Raw(`
		SELECT substr(created_at, 1, 10) AS date,
		       status,
		       COUNT(*) AS count
		FROM jobs
		WHERE substr(created_at, 1, 10) >= ?
		  AND status IN ('succeeded', 'failed')
		GROUP BY substr(created_at, 1, 10), status
		ORDER BY date ASC
	`, weekStart).Scan(&jobRows).Error; err != nil {
		return nil, fmt.Errorf("dashboard: job activity: %w", err)
	}

	// Build a map for quick lookup, then materialise the 7-day slice.
	type dayCounts struct{ succeeded, failed int64 }
	jobMap := make(map[string]dayCounts)
	for _, row := range jobRows {
		c := jobMap[row.Date]
		if row.Status == "succeeded" {
			c.succeeded = row.Count
		} else {
			c.failed = row.Count
		}
		jobMap[row.Date] = c
	}

	stats.JobActivity = make([]DayJobActivity, 7)
	for i := 0; i < 7; i++ {
		t := time.Now().UTC().AddDate(0, 0, -(6 - i))
		day := t.Format("2006-01-02")
		c := jobMap[day]
		stats.JobActivity[i] = DayJobActivity{
			Date:      day,
			Succeeded: c.succeeded,
			Failed:    c.failed,
		}
	}

	// ── Size activity — last 7 days ───────────────────────────────────────────
	// Sums size_bytes of snapshots created each day. Uses snapshot_at (the
	// actual backup timestamp) rather than created_at for semantic correctness.

	type sizeActivityRow struct {
		Date      string
		SizeBytes int64
	}

	var sizeRows []sizeActivityRow
	if err := d.Raw(`
		SELECT substr(snapshot_at, 1, 10) AS date,
		       COALESCE(SUM(size_bytes), 0) AS size_bytes
		FROM snapshots
		WHERE substr(snapshot_at, 1, 10) >= ?
		GROUP BY substr(snapshot_at, 1, 10)
		ORDER BY date ASC
	`, weekStart).Scan(&sizeRows).Error; err != nil {
		return nil, fmt.Errorf("dashboard: size activity: %w", err)
	}

	sizeMap := make(map[string]int64)
	for _, row := range sizeRows {
		sizeMap[row.Date] = row.SizeBytes
	}

	stats.SizeActivity = make([]DaySizeActivity, 7)
	for i := 0; i < 7; i++ {
		t := time.Now().UTC().AddDate(0, 0, -(6 - i))
		day := t.Format("2006-01-02")
		stats.SizeActivity[i] = DaySizeActivity{
			Date:      day,
			SizeBytes: sizeMap[day],
		}
	}

	return stats, nil
}