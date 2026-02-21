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
func (r *gormJobRepository) GetByIDWithDetails(ctx context.Context, id uuid.UUID) (*db.Job, []db.JobDestination, []db.JobLog, error) {
	var job db.Job
	err := r.db.WithContext(ctx).First(&job, "id = ?", id).Error
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

// UpdateStatus updates only the status, ended_at and error fields of a job.
// Called at the end of job execution to avoid overwriting fields updated
// during the run (e.g. destinations, logs).
func (r *gormJobRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string, endedAt *time.Time, errMsg string) error {
	result := r.db.WithContext(ctx).
		Model(&db.Job{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":   status,
			"ended_at": endedAt,
			"error":    errMsg,
		})
	if result.Error != nil {
		return fmt.Errorf("jobs: update status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns a paginated list of jobs and the total count,
// ordered by creation time descending (most recent first).
func (r *gormJobRepository) List(ctx context.Context, opts ListOptions) ([]db.Job, int64, error) {
	var jobs []db.Job
	var total int64

	if err := r.db.WithContext(ctx).Model(&db.Job{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list count: %w", err)
	}

	if err := r.db.WithContext(ctx).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Order("created_at DESC").
		Find(&jobs).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list: %w", err)
	}

	return jobs, total, nil
}

// ListByPolicy returns a paginated list of jobs for a given policy,
// ordered by creation time descending.
func (r *gormJobRepository) ListByPolicy(ctx context.Context, policyID uuid.UUID, opts ListOptions) ([]db.Job, int64, error) {
	var jobs []db.Job
	var total int64

	if err := r.db.WithContext(ctx).
		Model(&db.Job{}).
		Where("policy_id = ?", policyID).
		Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list by policy count: %w", err)
	}

	if err := r.db.WithContext(ctx).
		Where("policy_id = ?", policyID).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Order("created_at DESC").
		Find(&jobs).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list by policy: %w", err)
	}

	return jobs, total, nil
}

// ListByAgent returns a paginated list of jobs for a given agent,
// ordered by creation time descending.
func (r *gormJobRepository) ListByAgent(ctx context.Context, agentID uuid.UUID, opts ListOptions) ([]db.Job, int64, error) {
	var jobs []db.Job
	var total int64

	if err := r.db.WithContext(ctx).
		Model(&db.Job{}).
		Where("agent_id = ?", agentID).
		Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("jobs: list by agent count: %w", err)
	}

	if err := r.db.WithContext(ctx).
		Where("agent_id = ?", agentID).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Order("created_at DESC").
		Find(&jobs).Error; err != nil {
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