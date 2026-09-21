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

// gormDestinationRepository is the GORM implementation of DestinationRepository.
type gormDestinationRepository struct {
	db *gorm.DB
}

// NewDestinationRepository returns a DestinationRepository backed by the provided *gorm.DB.
func NewDestinationRepository(db *gorm.DB) DestinationRepository {
	return &gormDestinationRepository{db: db}
}

// Create inserts a new destination record into the database.
func (r *gormDestinationRepository) Create(ctx context.Context, destination *db.Destination) error {
	if err := r.db.WithContext(ctx).Create(destination).Error; err != nil {
		return fmt.Errorf("destinations: create: %w", err)
	}
	return nil
}

// GetByID retrieves a destination by its UUID.
// Returns ErrNotFound if no record exists.
func (r *gormDestinationRepository) GetByID(ctx context.Context, id uuid.UUID) (*db.Destination, error) {
	var destination db.Destination
	err := r.db.WithContext(ctx).First(&destination, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("destinations: get by id: %w", err)
	}
	return &destination, nil
}

// Update persists all fields of an existing destination record.
// Credentials are automatically re-encrypted by EncryptedString.Value()
// before being written to the database.
func (r *gormDestinationRepository) Update(ctx context.Context, destination *db.Destination) error {
	result := r.db.WithContext(ctx).Save(destination)
	if result.Error != nil {
		return fmt.Errorf("destinations: update: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateRepoSize updates only the cached restic repository size and its
// timestamp for a destination, without touching other fields (credentials,
// config, etc.). Used to refresh per-destination usage after a backup.
func (r *gormDestinationRepository) UpdateRepoSize(ctx context.Context, id uuid.UUID, sizeBytes int64, at time.Time) error {
	result := r.db.WithContext(ctx).
		Model(&db.Destination{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"repo_size_bytes":      sizeBytes,
			"repo_size_updated_at": at,
		})
	if result.Error != nil {
		return fmt.Errorf("destinations: update repo size: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete permanently removes a destination record by ID.
// Returns ErrNotFound if no record exists.
// Note: deletion will fail if the destination is still referenced by an active
// policy (FK constraint with ON DELETE RESTRICT). The caller should verify
// there are no active policy_destinations before deleting.
func (r *gormDestinationRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&db.Destination{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("destinations: delete: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// destinationOrderClause maps ListOptions.SortBy to a safe ORDER BY clause via a
// fixed whitelist, so the sort column can never be attacker-controlled SQL.
// Unknown or empty SortBy falls back to the historical default (created_at ASC).
func destinationOrderClause(opts ListOptions) string {
	col, ok := map[string]string{
		"usage":   "repo_size_bytes",
		"name":    "name",
		"created": "created_at",
		"type":    "type",
	}[opts.SortBy]
	if !ok {
		return "created_at ASC"
	}
	if opts.SortDesc {
		return col + " DESC"
	}
	return col + " ASC"
}

// ListFiltered returns a paginated list of destinations matching the given filter.
// If filter.Search is non-empty, only destinations whose name contains the search
// string (case-insensitive) are returned.
func (r *gormDestinationRepository) ListFiltered(ctx context.Context, filter DestinationFilter, opts ListOptions) ([]db.Destination, int64, error) {
	countQ := r.db.WithContext(ctx).Model(&db.Destination{})
	if filter.Search != "" {
		countQ = countQ.Where("name LIKE ?", "%"+filter.Search+"%")
	}
	var total int64
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("destinations: list filtered count: %w", err)
	}

	listQ := r.db.WithContext(ctx).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Order(destinationOrderClause(opts))
	if filter.Search != "" {
		listQ = listQ.Where("name LIKE ?", "%"+filter.Search+"%")
	}
	var destinations []db.Destination
	if err := listQ.Find(&destinations).Error; err != nil {
		return nil, 0, fmt.Errorf("destinations: list filtered: %w", err)
	}
	return destinations, total, nil
}

// ListPoliciesByDestination returns every live policy attached to a
// destination via policy_destinations, ordered by attachment time (earliest
// first) — used both by the retention scheduler (to know which restic tags
// to sweep) and by the one-time migration backfill (which treats the
// earliest-attached policy as the source of truth when a destination is
// shared).
func (r *gormDestinationRepository) ListPoliciesByDestination(ctx context.Context, destinationID uuid.UUID) ([]db.Policy, error) {
	var policies []db.Policy
	err := r.db.WithContext(ctx).
		Table("policies p").
		Select("p.*").
		Joins("INNER JOIN policy_destinations pd ON pd.policy_id = p.id").
		Where("pd.destination_id = ? AND p.deleted_at IS NULL", destinationID).
		Order("pd.created_at ASC").
		Scan(&policies).Error
	if err != nil {
		return nil, fmt.Errorf("destinations: list policies by destination: %w", err)
	}
	return policies, nil
}

// PolicyCountsByDestination returns, for every destination with at least one
// live policy attached, how many such policies there are.
func (r *gormDestinationRepository) PolicyCountsByDestination(ctx context.Context) (map[uuid.UUID]int64, error) {
	var rows []struct {
		DestinationID uuid.UUID
		Count         int64
	}
	err := r.db.WithContext(ctx).
		Table("policy_destinations pd").
		Select("pd.destination_id AS destination_id, COUNT(DISTINCT pd.policy_id) AS count").
		Joins("INNER JOIN policies p ON p.id = pd.policy_id").
		Where("p.deleted_at IS NULL").
		Group("pd.destination_id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("destinations: policy counts by destination: %w", err)
	}
	counts := make(map[uuid.UUID]int64, len(rows))
	for _, row := range rows {
		counts[row.DestinationID] = row.Count
	}
	return counts, nil
}

// ListWithRetentionSchedule returns every enabled, non-append-only
// destination with a configured retention schedule — the set the retention
// scheduler registers a gocron job for on Start.
func (r *gormDestinationRepository) ListWithRetentionSchedule(ctx context.Context) ([]db.Destination, error) {
	var destinations []db.Destination
	err := r.db.WithContext(ctx).
		Where("retention_enabled = ? AND append_only = ? AND retention_schedule != ''", true, false).
		Find(&destinations).Error
	if err != nil {
		return nil, fmt.Errorf("destinations: list with retention schedule: %w", err)
	}
	return destinations, nil
}

// TryAcquireBusy atomically claims the destination for jobID if it is not
// already busy. A false, nil-error return means another job already holds
// it — the caller should skip or defer this dispatch, not treat it as an
// error. Idempotent for the same jobID (matches if already held by jobID
// itself), so retrying a dispatch that previously acquired the gate — e.g.
// DispatchPending resending to an agent that just reconnected — never
// mistakes its own hold for contention.
func (r *gormDestinationRepository) TryAcquireBusy(ctx context.Context, destinationID, jobID uuid.UUID) (bool, error) {
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).
		Model(&db.Destination{}).
		Where("id = ? AND (busy_job_id IS NULL OR busy_job_id = ?)", destinationID, jobID).
		Updates(map[string]any{
			"busy_job_id": jobID,
			"busy_since":  now,
		})
	if result.Error != nil {
		return false, fmt.Errorf("destinations: try acquire busy: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

// ReleaseBusy clears the busy gate only if jobID is still the current
// holder, so a stale or duplicate release can never clear a newer lock.
func (r *gormDestinationRepository) ReleaseBusy(ctx context.Context, destinationID, jobID uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Model(&db.Destination{}).
		Where("id = ? AND busy_job_id = ?", destinationID, jobID).
		Updates(map[string]any{
			"busy_job_id": nil,
			"busy_since":  nil,
		})
	if result.Error != nil {
		return fmt.Errorf("destinations: release busy: %w", result.Error)
	}
	return nil
}

// ReleaseBusyForJobs bulk-releases the busy gate for a set of jobs, used by
// orphan recovery (an agent disconnected or the server restarted while a
// busy-holding job was running) so the gate can never get stuck.
func (r *gormDestinationRepository) ReleaseBusyForJobs(ctx context.Context, jobIDs []uuid.UUID) error {
	if len(jobIDs) == 0 {
		return nil
	}
	result := r.db.WithContext(ctx).
		Model(&db.Destination{}).
		Where("busy_job_id IN ?", jobIDs).
		Updates(map[string]any{
			"busy_job_id": nil,
			"busy_since":  nil,
		})
	if result.Error != nil {
		return fmt.Errorf("destinations: release busy for jobs: %w", result.Error)
	}
	return nil
}

// List returns a paginated list of destinations and the total count.
func (r *gormDestinationRepository) List(ctx context.Context, opts ListOptions) ([]db.Destination, int64, error) {
	var destinations []db.Destination
	var total int64

	if err := r.db.WithContext(ctx).Model(&db.Destination{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("destinations: list count: %w", err)
	}

	if err := r.db.WithContext(ctx).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Order(destinationOrderClause(opts)).
		Find(&destinations).Error; err != nil {
		return nil, 0, fmt.Errorf("destinations: list: %w", err)
	}

	return destinations, total, nil
}
