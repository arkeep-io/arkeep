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

// GetByIDWithDetails retrieves a job together with its JobDestination and
// JobLog records using three separate queries. All values are returned
// independently rather than embedded in the Job struct, because GORM cannot
// auto-resolve UUID-typed foreign keys (see db/models.go for rationale).
//
// Logs are ordered by timestamp ascending so the caller can replay execution
// order without additional sorting.
func (r *gormJobRepository) GetByIDWithDetails(ctx context.Context, id uuid.UUID) (*JobWithNames, []db.JobDestination, []db.JobLog, error) {
	var job JobWithNames
	err := r.db.WithContext(ctx).
		Table("jobs").
		Select("jobs.*, policies.name as policy_name, agents.name as agent_name").
		Joins("JOIN policies ON policies.id = jobs.policy_id AND policies.deleted_at IS NULL").
		Joins("JOIN agents ON agents.id = jobs.agent_id AND agents.deleted_at IS NULL").
		Where("jobs.id = ?", id).
		First(&job).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil, ErrNotFound
		}
		return nil, nil, nil, fmt.Errorf("jobs: get by id with details: %w", err)
	}

	var destinations []db.JobDestination
	if err := r.db.WithContext(ctx).
		Where("job_id = ?", id).
		Find(&destinations).Error; err != nil {
		return nil, nil, nil, fmt.Errorf("jobs: get destinations for job %s: %w", id, err)
	}

	var logs []db.JobLog
	if err := r.db.WithContext(ctx).
		Where("job_id = ?", id).
		Order("timestamp ASC").
		Find(&logs).Error; err != nil {
		return nil, nil, nil, fmt.Errorf("jobs: get logs for job %s: %w", id, err)
	}

	return &job, destinations, logs, nil
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

// UpdateStatus updates the status, started_at, ended_at and error fields of a
// job. Called by the gRPC server on each agent status report:
//   - running:   startedAt = &now, endedAt = nil
//   - succeeded: startedAt = nil (already set), endedAt = &now
//   - failed:    startedAt = nil (already set), endedAt = &now
//
// A nil pointer is skipped by GORM — passing nil for startedAt on terminal
// transitions preserves the value already written when the job started running.
func (r *gormJobRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string, startedAt *time.Time, endedAt *time.Time, errMsg string) error {
	updates := map[string]interface{}{
		"status": status,
		"error":  errMsg,
	}
	// Only set started_at when explicitly provided — avoids overwriting the
	// value on subsequent status transitions (running → succeeded/failed).
	if startedAt != nil {
		updates["started_at"] = startedAt
	}
	// Only set ended_at when explicitly provided — avoids nullifying it on
	// earlier transitions (pending → running).
	if endedAt != nil {
		updates["ended_at"] = endedAt
	}

	result := r.db.WithContext(ctx).
		Model(&db.Job{}).
		Where("id = ?", id).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("jobs: update status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

const jobJoins = "JOIN policies ON policies.id = jobs.policy_id AND policies.deleted_at IS NULL " +
	"JOIN agents ON agents.id = jobs.agent_id AND agents.deleted_at IS NULL"

// List returns a paginated list of jobs and the total count,
// ordered by creation time descending (most recent first).
func (r *gormJobRepository) List(ctx context.Context, opts ListOptions) ([]JobWithNames, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).Table("jobs").Joins(jobJoins).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list count: %w", err)
	}

	var jobs []JobWithNames
	if err := r.db.WithContext(ctx).
		Table("jobs").
		Select("jobs.*, policies.name as policy_name, agents.name as agent_name").
		Joins(jobJoins).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Order("jobs.created_at DESC").
		Scan(&jobs).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list: %w", err)
	}

	return jobs, total, nil
}

// ListByPolicy returns a paginated list of jobs for a given policy,
// ordered by creation time descending.
func (r *gormJobRepository) ListByPolicy(ctx context.Context, policyID uuid.UUID, opts ListOptions) ([]JobWithNames, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).
		Table("jobs").
		Joins(jobJoins).
		Where("jobs.policy_id = ?", policyID).
		Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list by policy count: %w", err)
	}

	var jobs []JobWithNames
	if err := r.db.WithContext(ctx).
		Table("jobs").
		Select("jobs.*, policies.name as policy_name, agents.name as agent_name").
		Joins(jobJoins).
		Where("jobs.policy_id = ?", policyID).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Order("jobs.created_at DESC").
		Scan(&jobs).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list by policy: %w", err)
	}

	return jobs, total, nil
}

// ListByAgent returns a paginated list of jobs for a given agent,
// ordered by creation time descending.
func (r *gormJobRepository) ListByAgent(ctx context.Context, agentID uuid.UUID, opts ListOptions) ([]JobWithNames, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).
		Table("jobs").
		Joins(jobJoins).
		Where("jobs.agent_id = ?", agentID).
		Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list by agent count: %w", err)
	}

	var jobs []JobWithNames
	if err := r.db.WithContext(ctx).
		Table("jobs").
		Select("jobs.*, policies.name as policy_name, agents.name as agent_name").
		Joins(jobJoins).
		Where("jobs.agent_id = ?", agentID).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Order("jobs.created_at DESC").
		Scan(&jobs).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list by agent: %w", err)
	}

	return jobs, total, nil
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

// ListDestinationsByJob returns all JobDestination records for a given job.
// Used internally by GetByIDWithDetails and directly by callers that need
// only destinations without the full job detail view.
func (r *gormJobRepository) ListDestinationsByJob(ctx context.Context, jobID uuid.UUID) ([]db.JobDestination, error) {
	var destinations []db.JobDestination
	if err := r.db.WithContext(ctx).
		Where("job_id = ?", jobID).
		Find(&destinations).Error; err != nil {
		return nil, fmt.Errorf("jobs: list destinations by job: %w", err)
	}
	return destinations, nil
}

// UpdateDestinationStatus updates the result fields of a job destination
// after the backup to that destination completes or fails.
func (r *gormJobRepository) UpdateDestinationStatus(ctx context.Context, id uuid.UUID, status string, endedAt *time.Time, snapshotID string, sizeBytes int64, errMsg string) error {
	result := r.db.WithContext(ctx).
		Model(&db.JobDestination{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":      status,
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