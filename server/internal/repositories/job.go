package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// gormJobRepository is the GORM implementation of JobRepository.
type gormJobRepository struct {
	db *gorm.DB
}

// NewJobRepository returns a JobRepository backed by the provided *gorm.DB.
func NewJobRepository(db *gorm.DB) JobRepository {
	return &gormJobRepository{db: db}
}

// Create inserts a new job record into the database.
func (r *gormJobRepository) Create(ctx context.Context, job *db.Job) error {
	if err := r.db.WithContext(ctx).Create(job).Error; err != nil {
		return fmt.Errorf("jobs: create: %w", err)
	}
	return nil
}

// GetByID retrieves a job by its UUID.
// Returns ErrNotFound if no record exists.
func (r *gormJobRepository) GetByID(ctx context.Context, id uuid.UUID) (*db.Job, error) {
	var job db.Job
	err := r.db.WithContext(ctx).First(&job, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("jobs: get by id: %w", err)
	}
	return &job, nil
}

// GetByIDWithDetails retrieves a job (with policy and agent names) together
// with its JobDestination and JobLog records. Names are resolved via LEFT JOIN
// so the response is display-ready without additional lookups. All values are
// returned independently rather than embedded in the Job struct, because GORM
// cannot auto-resolve UUID-typed foreign keys (see db/models.go for rationale).
//
// Logs are ordered by timestamp ascending so the caller can replay execution
// order without additional sorting.
func (r *gormJobRepository) GetByIDWithDetails(ctx context.Context, id uuid.UUID) (*JobWithNames, []JobDestinationWithName, []db.JobLog, error) {
	var row JobWithNames
	err := r.db.WithContext(ctx).
		Model(&db.Job{}).
		Select(listJobsJoin).
		Joins("LEFT JOIN policies ON policies.id = jobs.policy_id AND policies.deleted_at IS NULL").
		Joins("LEFT JOIN agents ON agents.id = jobs.agent_id AND agents.deleted_at IS NULL").
		Where("jobs.id = ?", id).
		Scan(&row).Error
	if err != nil {
		return nil, nil, nil, fmt.Errorf("jobs: get by id with details: %w", err)
	}
	// GORM Scan does not set ErrRecordNotFound — detect a missing row via zero UUID.
	if row.ID == (uuid.UUID{}) {
		return nil, nil, nil, ErrNotFound
	}

	var destinations []JobDestinationWithName
	if err := r.db.WithContext(ctx).
		Model(&db.JobDestination{}).
		Select(listDestinationsJoin).
		Joins("LEFT JOIN destinations ON destinations.id = job_destinations.destination_id").
		Where("job_id = ?", id).
		Scan(&destinations).Error; err != nil {
		return nil, nil, nil, fmt.Errorf("jobs: get destinations for job %s: %w", id, err)
	}

	var logs []db.JobLog
	if err := r.db.WithContext(ctx).
		Where("job_id = ?", id).
		Order("timestamp ASC").
		Find(&logs).Error; err != nil {
		return nil, nil, nil, fmt.Errorf("jobs: get logs for job %s: %w", id, err)
	}

	return &row, destinations, logs, nil
}

// Update persists all fields of an existing job record.
func (r *gormJobRepository) Update(ctx context.Context, job *db.Job) error {
	result := r.db.WithContext(ctx).Save(job)
	if result.Error != nil {
		return fmt.Errorf("jobs: update: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// terminalJobStatuses are the statuses a job never moves out of.
var terminalJobStatuses = []string{"succeeded", "failed", "cancelled", "interrupted"}

// UpdateStatus updates the status, started_at, ended_at and error fields of a
// job. Called by the gRPC server on each agent status report:
//   - running:   startedAt = &now, endedAt = nil
//   - succeeded: startedAt = nil (already set), endedAt = &now
//   - failed:    startedAt = nil (already set), endedAt = &now
//
// A nil pointer is skipped — passing nil for startedAt on terminal transitions
// preserves the value already written when the job started running.
//
// A job already in a terminal status is never modified: it returns
// ErrTerminalState and writes nothing, so a report that arrives after the server
// gave up on the job cannot resurrect it. Returns ErrNotFound if no such job
// exists.
func (r *gormJobRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string, startedAt *time.Time, endedAt *time.Time, errMsg string) error {
	updates := map[string]interface{}{
		"status": status,
		"error":  errMsg,
	}
	if startedAt != nil {
		updates["started_at"] = startedAt
	}
	if endedAt != nil {
		updates["ended_at"] = endedAt
	}

	result := r.db.WithContext(ctx).
		Model(&db.Job{}).
		Where("id = ? AND status NOT IN ?", id, terminalJobStatuses).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("jobs: update status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// Tell "already finished" apart from "does not exist" — the caller logs
		// them very differently.
		var count int64
		if err := r.db.WithContext(ctx).Model(&db.Job{}).Where("id = ?", id).Count(&count).Error; err != nil {
			return fmt.Errorf("jobs: update status: %w", err)
		}
		if count == 0 {
			return ErrNotFound
		}
		return ErrTerminalState
	}
	return nil
}

// MarkRunningJobsInterruptedForAgent marks all jobs in "running" state for the
// given agent as "interrupted" with the provided error message. Called during
// agent disconnection cleanup to recover orphaned jobs that would otherwise be
// stuck in "running" forever.
//
// "interrupted" rather than "failed" is what makes these jobs eligible for
// automatic resume: the agent vanished, it did not report an error. The
// status = "running" filter leaves a job the user cancelled untouched, since the
// cancel already moved it to a terminal state.
//
// Non-terminal destination rows of the same jobs are moved along with them,
// otherwise the job detail view shows destinations still "pending" underneath a
// finished job.
//
// Returns the number of jobs updated.
func (r *gormJobRepository) MarkRunningJobsInterruptedForAgent(ctx context.Context, agentID uuid.UUID, errMsg string) (int64, error) {
	now := time.Now().UTC()

	var updated int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Collect the affected job IDs first: the destination rows have to be
		// found before the parent rows stop matching status = "running".
		var jobIDs []uuid.UUID
		if err := tx.Model(&db.Job{}).
			Where("agent_id = ? AND status = ?", agentID, "running").
			Pluck("id", &jobIDs).Error; err != nil {
			return fmt.Errorf("select running jobs: %w", err)
		}
		if len(jobIDs) == 0 {
			return nil
		}

		result := tx.Model(&db.Job{}).
			Where("id IN ?", jobIDs).
			Updates(map[string]interface{}{
				"status":   "interrupted",
				"ended_at": now,
				"error":    errMsg,
			})
		if result.Error != nil {
			return fmt.Errorf("update jobs: %w", result.Error)
		}
		updated = result.RowsAffected

		if err := tx.Model(&db.JobDestination{}).
			Where("job_id IN ? AND status IN ?", jobIDs, []string{"pending", "running"}).
			Updates(map[string]interface{}{
				"status":   "interrupted",
				"ended_at": now,
				"error":    errMsg,
			}).Error; err != nil {
			return fmt.Errorf("update job destinations: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("jobs: mark running interrupted for agent: %w", err)
	}
	return updated, nil
}

// MarkResumeExhausted moves an interrupted job to "failed" with errMsg, once the
// automatic resume has given up on it.
//
// The transition matters for more than presentation: it takes the job out of the
// set scanned on every reconnect, so the give-up notification fires once instead
// of on every reconnection until the next scheduled run. The status = "interrupted"
// filter makes it idempotent. Returns ErrNotFound if the job is not (or no longer)
// interrupted.
func (r *gormJobRepository) MarkResumeExhausted(ctx context.Context, id uuid.UUID, errMsg string) error {
	result := r.db.WithContext(ctx).
		Model(&db.Job{}).
		Where("id = ? AND status = ?", id, "interrupted").
		Updates(map[string]interface{}{
			"status": "failed",
			"error":  errMsg,
		})
	if result.Error != nil {
		return fmt.Errorf("jobs: mark resume exhausted: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkRunningJobsInterrupted does the same for every agent. Called once at server
// startup: a job that was running when the process died has no stream teardown
// to recover it and would stay "running" forever.
//
// Returns the number of jobs updated.
func (r *gormJobRepository) MarkRunningJobsInterrupted(ctx context.Context, errMsg string) (int64, error) {
	now := time.Now().UTC()

	var updated int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var jobIDs []uuid.UUID
		if err := tx.Model(&db.Job{}).
			Where("status = ?", "running").
			Pluck("id", &jobIDs).Error; err != nil {
			return fmt.Errorf("select running jobs: %w", err)
		}
		if len(jobIDs) == 0 {
			return nil
		}

		result := tx.Model(&db.Job{}).
			Where("id IN ?", jobIDs).
			Updates(map[string]interface{}{
				"status":   "interrupted",
				"ended_at": now,
				"error":    errMsg,
			})
		if result.Error != nil {
			return fmt.Errorf("update jobs: %w", result.Error)
		}
		updated = result.RowsAffected

		if err := tx.Model(&db.JobDestination{}).
			Where("job_id IN ? AND status IN ?", jobIDs, []string{"pending", "running"}).
			Updates(map[string]interface{}{
				"status":   "interrupted",
				"ended_at": now,
				"error":    errMsg,
			}).Error; err != nil {
			return fmt.Errorf("update job destinations: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("jobs: mark running interrupted: %w", err)
	}
	return updated, nil
}

// JobWithNames extends db.Job with denormalised policy and agent names.
// Populated via LEFT JOIN in the List* methods so the API can return
// display-ready responses without per-row lookups. LEFT JOIN ensures jobs
// whose policy or agent has been soft-deleted still appear (names = "").
type JobWithNames struct {
	db.Job
	PolicyName string
	AgentName  string
}

// JobDestinationWithName extends db.JobDestination with the destination's
// display name, resolved via LEFT JOIN in ListDestinationsByJob.
// LEFT JOIN ensures rows survive even if the destination was deleted.
type JobDestinationWithName struct {
	db.JobDestination
	DestinationName string
}

// listDestinationsJoin is the shared SELECT fragment for destination queries.
const listDestinationsJoin = `job_destinations.*,
	COALESCE(destinations.name, '') AS destination_name`
// Extracted as a constant to avoid repetition and keep the join clause in sync.
const listJobsJoin = `jobs.*,
	COALESCE(policies.name, '') AS policy_name,
	COALESCE(agents.name, '')   AS agent_name`

// List returns a paginated list of jobs (with policy and agent names) and the
// total count, ordered by creation time descending (most recent first).
func (r *gormJobRepository) List(ctx context.Context, opts ListOptions) ([]JobWithNames, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).Model(&db.Job{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list count: %w", err)
	}

	var rows []JobWithNames
	if err := r.db.WithContext(ctx).
		Model(&db.Job{}).
		Select(listJobsJoin).
		Joins("LEFT JOIN policies ON policies.id = jobs.policy_id AND policies.deleted_at IS NULL").
		Joins("LEFT JOIN agents ON agents.id = jobs.agent_id AND agents.deleted_at IS NULL").
		Limit(opts.Limit).
		Offset(opts.Offset).
		Order("jobs.created_at DESC").
		Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list: %w", err)
	}

	return rows, total, nil
}

// ListByPolicy returns a paginated list of jobs for a given policy,
// with policy and agent names, ordered by creation time descending.
func (r *gormJobRepository) ListByPolicy(ctx context.Context, policyID uuid.UUID, opts ListOptions) ([]JobWithNames, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).Model(&db.Job{}).Where("policy_id = ?", policyID).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list by policy count: %w", err)
	}

	var rows []JobWithNames
	if err := r.db.WithContext(ctx).
		Model(&db.Job{}).
		Select(listJobsJoin).
		Joins("LEFT JOIN policies ON policies.id = jobs.policy_id AND policies.deleted_at IS NULL").
		Joins("LEFT JOIN agents ON agents.id = jobs.agent_id AND agents.deleted_at IS NULL").
		Where("jobs.policy_id = ?", policyID).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Order("jobs.created_at DESC").
		Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list by policy: %w", err)
	}

	return rows, total, nil
}

// ListByAgent returns a paginated list of jobs for a given agent,
// with policy and agent names, ordered by creation time descending.
func (r *gormJobRepository) ListByAgent(ctx context.Context, agentID uuid.UUID, opts ListOptions) ([]JobWithNames, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).Model(&db.Job{}).Where("agent_id = ?", agentID).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list by agent count: %w", err)
	}

	var rows []JobWithNames
	if err := r.db.WithContext(ctx).
		Model(&db.Job{}).
		Select(listJobsJoin).
		Joins("LEFT JOIN policies ON policies.id = jobs.policy_id AND policies.deleted_at IS NULL").
		Joins("LEFT JOIN agents ON agents.id = jobs.agent_id AND agents.deleted_at IS NULL").
		Where("jobs.agent_id = ?", agentID).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Order("jobs.created_at DESC").
		Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list by agent: %w", err)
	}

	return rows, total, nil
}

// ListByAgentAndStatus returns jobs for an agent that are in the given status,
// oldest first, with policy and agent names.
//
// Filtering in SQL matters here: callers that reach for ListByAgent and filter in
// Go only ever see the most recent page of jobs, so on an agent with a long
// history an older job in the wanted status is silently never found.
// Oldest-first because these are work queues — a pending job created earlier
// should be dispatched, and an interruption resumed, before a later one.
func (r *gormJobRepository) ListByAgentAndStatus(ctx context.Context, agentID uuid.UUID, jobStatus string, opts ListOptions) ([]JobWithNames, error) {
	var rows []JobWithNames
	if err := r.db.WithContext(ctx).
		Model(&db.Job{}).
		Select(listJobsJoin).
		Joins("LEFT JOIN policies ON policies.id = jobs.policy_id AND policies.deleted_at IS NULL").
		Joins("LEFT JOIN agents ON agents.id = jobs.agent_id AND agents.deleted_at IS NULL").
		Where("jobs.agent_id = ? AND jobs.status = ?", agentID, jobStatus).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Order("jobs.created_at ASC").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("jobs: list by agent and status: %w", err)
	}
	return rows, nil
}

// HasJobForPolicyAfter reports whether the policy has a job created strictly
// after the given time. Used to decide against resuming an interrupted backup
// that a later scheduled run has already superseded.
func (r *gormJobRepository) HasJobForPolicyAfter(ctx context.Context, policyID uuid.UUID, after time.Time) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&db.Job{}).
		Where("policy_id = ? AND created_at > ?", policyID, after).
		Count(&count).Error; err != nil {
		return false, fmt.Errorf("jobs: has job for policy after: %w", err)
	}
	return count > 0, nil
}

// ListFiltered returns a paginated list of jobs filtered by any combination of
// status and type. Zero-valued fields in filter are ignored (no constraint added).
func (r *gormJobRepository) ListFiltered(ctx context.Context, filter JobFilter, opts ListOptions) ([]JobWithNames, int64, error) {
	countQ := r.db.WithContext(ctx).Model(&db.Job{})
	if filter.Status != "" {
		countQ = countQ.Where("status = ?", filter.Status)
	}
	if filter.Type != "" {
		countQ = countQ.Where("type = ?", filter.Type)
	}

	var total int64
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list filtered count: %w", err)
	}

	listQ := r.db.WithContext(ctx).
		Model(&db.Job{}).
		Select(listJobsJoin).
		Joins("LEFT JOIN policies ON policies.id = jobs.policy_id AND policies.deleted_at IS NULL").
		Joins("LEFT JOIN agents ON agents.id = jobs.agent_id AND agents.deleted_at IS NULL").
		Limit(opts.Limit).
		Offset(opts.Offset).
		Order("jobs.created_at DESC")
	if filter.Status != "" {
		listQ = listQ.Where("jobs.status = ?", filter.Status)
	}
	if filter.Type != "" {
		listQ = listQ.Where("jobs.type = ?", filter.Type)
	}

	var rows []JobWithNames
	if err := listQ.Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list filtered: %w", err)
	}

	return rows, total, nil
}

// ListByType returns a paginated list of jobs filtered by type ("backup" or
// "restore"), with policy and agent names, ordered by creation time descending.
func (r *gormJobRepository) ListByType(ctx context.Context, jobType string, opts ListOptions) ([]JobWithNames, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).Model(&db.Job{}).Where("type = ?", jobType).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list by type count: %w", err)
	}

	var rows []JobWithNames
	if err := r.db.WithContext(ctx).
		Model(&db.Job{}).
		Select(listJobsJoin).
		Joins("LEFT JOIN policies ON policies.id = jobs.policy_id AND policies.deleted_at IS NULL").
		Joins("LEFT JOIN agents ON agents.id = jobs.agent_id AND agents.deleted_at IS NULL").
		Where("jobs.type = ?", jobType).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Order("jobs.created_at DESC").
		Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list by type: %w", err)
	}

	return rows, total, nil
}

// -----------------------------------------------------------------------------
// JobDestination
// -----------------------------------------------------------------------------

// CreateDestination inserts a new job destination record.
// Called once per destination when a job is created.
func (r *gormJobRepository) CreateDestination(ctx context.Context, jd *db.JobDestination) error {
	if err := r.db.WithContext(ctx).Create(jd).Error; err != nil {
		return fmt.Errorf("jobs: create destination: %w", err)
	}
	return nil
}

// ListDestinationsByJob returns all JobDestination records for a given job,
// with the destination display name resolved via LEFT JOIN.
func (r *gormJobRepository) ListDestinationsByJob(ctx context.Context, jobID uuid.UUID) ([]JobDestinationWithName, error) {
	var destinations []JobDestinationWithName
	if err := r.db.WithContext(ctx).
		Model(&db.JobDestination{}).
		Select(listDestinationsJoin).
		Joins("LEFT JOIN destinations ON destinations.id = job_destinations.destination_id").
		Where("job_id = ?", jobID).
		Scan(&destinations).Error; err != nil {
		return nil, fmt.Errorf("jobs: list destinations by job: %w", err)
	}
	return destinations, nil
}

// UpdateDestinationStatus updates the result fields of a job destination
// after the backup to that destination completes or fails.
// The row is identified by (jobID, destID) rather than the surrogate PK to
// avoid the fragile two-step: scan PK via JOIN then UPDATE by PK.
func (r *gormJobRepository) UpdateDestinationStatus(ctx context.Context, jobID uuid.UUID, destID uuid.UUID, status string, startedAt *time.Time, endedAt *time.Time, snapshotID string, sizeBytes int64, errMsg string) error {
	result := r.db.WithContext(ctx).
		Model(&db.JobDestination{}).
		Where("job_id = ? AND destination_id = ?", jobID, destID).
		Updates(map[string]interface{}{
			"status":      status,
			"started_at":  startedAt,
			"ended_at":    endedAt,
			"snapshot_id": snapshotID,
			"size_bytes":  sizeBytes,
			"error":       errMsg,
		})
	if result.Error != nil {
		return fmt.Errorf("jobs: update destination status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// -----------------------------------------------------------------------------
// JobLog
// -----------------------------------------------------------------------------

// BulkCreateLogs inserts multiple log lines in a single database transaction.
// Logs are collected during job execution and inserted all at once at
// completion to minimize write pressure during the backup run.
func (r *gormJobRepository) BulkCreateLogs(ctx context.Context, logs []db.JobLog) error {
	if len(logs) == 0 {
		return nil
	}
	if err := r.db.WithContext(ctx).Create(&logs).Error; err != nil {
		return fmt.Errorf("jobs: bulk create logs: %w", err)
	}
	return nil
}

// HasPendingJob reports whether any job with status "pending" exists for the
// given policy. Used by the scheduler to avoid creating duplicate pending jobs
// when the agent is offline: at most one pending job per policy is queued.
func (r *gormJobRepository) HasPendingJob(ctx context.Context, policyID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&db.Job{}).
		Where("policy_id = ? AND status = ?", policyID, "pending").
		Limit(1).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("jobs: has pending job: %w", err)
	}
	return count > 0, nil
}

// GetLogs returns all log lines for a job ordered by timestamp ascending.
// Used to replay the full execution log in the job detail view.
func (r *gormJobRepository) GetLogs(ctx context.Context, jobID uuid.UUID) ([]db.JobLog, error) {
	var logs []db.JobLog
	if err := r.db.WithContext(ctx).
		Where("job_id = ?", jobID).
		Order("timestamp ASC").
		Find(&logs).Error; err != nil {
		return nil, fmt.Errorf("jobs: get logs: %w", err)
	}
	return logs, nil
}

// PruneLogsByLevel deletes job_logs rows matching the given levels and older
// than before, in batches of batchSize, and returns the total number of rows
// deleted. Deleting in batches keeps each statement short so the SQLite single
// writer is not blocked for the whole (potentially very large) first cleanup.
// The delete targets the indexed timestamp column (idx_job_logs_timestamp).
//
// The associated jobs are never touched — only their verbose log lines — so the
// job history in the jobs table is preserved.
func (r *gormJobRepository) PruneLogsByLevel(ctx context.Context, levels []string, before time.Time, batchSize int) (int64, error) {
	if len(levels) == 0 || batchSize <= 0 {
		return 0, nil
	}

	var total int64
	for {
		res := r.db.WithContext(ctx).Exec(
			`DELETE FROM job_logs WHERE id IN (
				SELECT id FROM job_logs WHERE level IN ? AND timestamp < ? LIMIT ?
			)`,
			levels, before, batchSize,
		)
		if res.Error != nil {
			return total, fmt.Errorf("jobs: prune logs: %w", res.Error)
		}
		total += res.RowsAffected
		// A short (or empty) batch means there is nothing left to delete.
		if res.RowsAffected < int64(batchSize) {
			break
		}
	}
	return total, nil
}

// ReclaimLogSpace returns disk space freed by pruning back to the filesystem.
// It is driver-aware because the two engines reclaim space differently:
//   - sqlite:   VACUUM rewrites the whole database file; it is the only way in
//     SQLite to actually shrink the .db file on disk.
//   - postgres: VACUUM (ANALYZE) job_logs marks the dead tuples reusable and
//     refreshes planner stats without taking an exclusive lock. VACUUM FULL is
//     deliberately avoided so it stays safe to run on a live production server.
//
// VACUUM cannot run inside a transaction, so the statement is executed with the
// default transaction wrapping disabled.
func (r *gormJobRepository) ReclaimLogSpace(ctx context.Context) error {
	tx := r.db.WithContext(ctx).Session(&gorm.Session{SkipDefaultTransaction: true})
	switch r.db.Name() {
	case "sqlite":
		if err := tx.Exec("VACUUM").Error; err != nil {
			return fmt.Errorf("jobs: vacuum sqlite: %w", err)
		}
	case "postgres":
		if err := tx.Exec("VACUUM (ANALYZE) job_logs").Error; err != nil {
			return fmt.Errorf("jobs: vacuum postgres: %w", err)
		}
	}
	return nil
}

