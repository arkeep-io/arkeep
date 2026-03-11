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

// UpdateStatus updates the status, started_at, ended_at and error fields of a
// job. Called by the gRPC server on each agent status report:
//   - running:   startedAt = &now, endedAt = nil
//   - succeeded: startedAt = nil (already set), endedAt = &now
//   - failed:    startedAt = nil (already set), endedAt = &now
//
// A nil pointer is skipped — passing nil for startedAt on terminal transitions
// preserves the value already written when the job started running.
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