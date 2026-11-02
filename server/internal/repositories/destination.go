package repositories

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

// GetByIDIncludingDeleted retrieves a destination by its UUID, soft-deleted
// rows included. Used where a deleted destination's non-sensitive fields
// (type, config) are still needed, e.g. restoring a snapshot with
// caller-supplied credentials. Returns ErrNotFound if no record exists.
func (r *gormDestinationRepository) GetByIDIncludingDeleted(ctx context.Context, id uuid.UUID) (*db.Destination, error) {
	var destination db.Destination
	err := r.db.WithContext(ctx).Unscoped().First(&destination, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("destinations: get by id including deleted: %w", err)
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

// Delete soft-deletes a destination by ID and wipes its stored secrets
// (credentials and repository password) in the same transaction. The row
// itself is kept so snapshots and job history keep resolving the destination's
// name, type and config, but nothing sensitive outlives the deletion: restoring
// a snapshot of a deleted destination requires the caller to supply the
// credentials again.
// Returns ErrNotFound if no live record exists.
func (r *gormDestinationRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&db.Destination{}).
			Where("id = ?", id).
			Updates(map[string]any{
				"credentials":   "",
				"repo_password": "",
			})
		if result.Error != nil {
			return fmt.Errorf("destinations: delete: wipe secrets: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		if err := tx.Delete(&db.Destination{}, "id = ?", id).Error; err != nil {
			return fmt.Errorf("destinations: delete: %w", err)
		}
		return nil
	})
}

// destinationSortColumns are the sortable columns of the destinations list.
var destinationSortColumns = map[string]sortColumn{
	"usage":   {exprs: []string{"repo_size_bytes"}},
	"name":    {exprs: []string{"name"}},
	"created": {exprs: []string{"created_at"}},
	"type":    {exprs: []string{"type"}},
}

// destinationOrderClause maps ListOptions.SortBy to a safe ORDER BY clause (see
// orderClause). Unknown or empty SortBy falls back to the historical default
// (created_at ASC).
func destinationOrderClause(opts ListOptions) string {
	return orderClause(opts, destinationSortColumns, "created_at ASC", "id ASC")
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
// destination with retention enabled and a configured retention schedule —
// the set the retention scheduler registers a gocron job for on Start.
func (r *gormDestinationRepository) ListWithRetentionSchedule(ctx context.Context) ([]db.Destination, error) {
	var destinations []db.Destination
	err := r.db.WithContext(ctx).
		Where("enabled = ? AND retention_enabled = ? AND append_only = ? AND retention_schedule != ''", true, true, false).
		Find(&destinations).Error
	if err != nil {
		return nil, fmt.Errorf("destinations: list with retention schedule: %w", err)
	}
	return destinations, nil
}

// ListWithCheckSchedule returns every enabled destination with integrity
// checks enabled and a configured schedule — the set the check scheduler
// registers a gocron job for on Start. Append-only destinations are included:
// restic check only reads the repository.
func (r *gormDestinationRepository) ListWithCheckSchedule(ctx context.Context) ([]db.Destination, error) {
	var destinations []db.Destination
	err := r.db.WithContext(ctx).
		Where("enabled = ? AND check_enabled = ? AND check_schedule != ''", true, true).
		Find(&destinations).Error
	if err != nil {
		return nil, fmt.Errorf("destinations: list with check schedule: %w", err)
	}
	return destinations, nil
}

// UpdateLastCheck records the outcome of a destination's most recent
// integrity check job. A missing (e.g. deleted) destination is not an error:
// there is nothing left to record the outcome on.
func (r *gormDestinationRepository) UpdateLastCheck(ctx context.Context, destinationID, jobID uuid.UUID, status string, at time.Time) error {
	err := r.db.WithContext(ctx).
		Model(&db.Destination{}).
		Where("id = ?", destinationID).
		Updates(map[string]interface{}{
			"last_check_at":     at,
			"last_check_status": status,
			"last_check_job_id": jobID,
		}).Error
	if err != nil {
		return fmt.Errorf("destinations: update last check: %w", err)
	}
	return nil
}

// lockHolderActive matches a destination_locks row whose job can still release
// it: pending, waiting or running. The hold of a job that finished, was
// cancelled or was interrupted without its release reaching the lock is stale
// (issue #290): it is ignored, and cleared by the next claim on the destination.
//
// A waiting job counts as active: the queue dispatcher claims a queued job's
// destinations just before moving it back to pending (issue #285), and a hold
// caught between the two must not be taken over. Should the move never happen,
// the job still re-claims its own hold on the next drain (TryAcquireBusyAll is
// idempotent for the holder), and the queue timeout ends it otherwise.
const lockHolderActive = `EXISTS (SELECT 1 FROM jobs WHERE jobs.id = destination_locks.job_id AND jobs.status IN ('pending', 'waiting', 'running'))`

// TryAcquireBusyAll claims every destination in destinationIDs for jobID in
// mode (db.LockShared or db.LockExclusive), or none of them: a job that needs
// several destinations must never hold some while waiting for the rest,
// otherwise two jobs each holding what the other needs would wait forever
// (issue #285). A shared claim only conflicts with an exclusive hold, an
// exclusive claim with any hold (issue #319). A false, nil-error return means
// at least one destination is held in a conflicting mode and nothing was
// claimed. Idempotent for the holder, so a dispatch retried for the same job —
// DispatchPending re-sending to an agent that just reconnected — never mistakes
// its own hold for contention.
func (r *gormDestinationRepository) TryAcquireBusyAll(ctx context.Context, destinationIDs []uuid.UUID, jobID uuid.UUID, mode string) (bool, error) {
	if len(destinationIDs) == 0 {
		return true, nil
	}
	ids := slices.Clone(destinationIDs)
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	ids = slices.Compact(ids)

	errBusy := errors.New("destination busy")
	now := time.Now().UTC()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock the destination rows, in ID order, so concurrent claims on the
		// same destinations take turns (PostgreSQL; SQLite has a single
		// connection and serialises them anyway).
		var locked []uuid.UUID
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Model(&db.Destination{}).
			Where("id IN ?", ids).
			Order("id").
			Pluck("id", &locked).Error; err != nil {
			return err
		}
		if err := tx.Where("destination_id IN ? AND NOT "+lockHolderActive, ids).
			Delete(&db.DestinationLock{}).Error; err != nil {
			return err
		}

		conflicts := tx.Model(&db.DestinationLock{}).Where("destination_id IN ? AND job_id <> ?", ids, jobID)
		if mode == db.LockShared {
			conflicts = conflicts.Where("mode = ?", db.LockExclusive)
		}
		var n int64
		if err := conflicts.Limit(1).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return errBusy
		}

		locks := make([]db.DestinationLock, 0, len(ids))
		for _, id := range ids {
			locks = append(locks, db.DestinationLock{DestinationID: id, JobID: jobID, Mode: mode, AcquiredAt: now})
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&locks).Error
	})
	if errors.Is(err, errBusy) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("destinations: try acquire busy all: %w", err)
	}
	return true, nil
}

// ReleaseBusy releases jobID's hold on the destination. Releasing a hold the
// job does not have is a no-op, so a stale or duplicate release can never free
// another job's hold.
func (r *gormDestinationRepository) ReleaseBusy(ctx context.Context, destinationID, jobID uuid.UUID) error {
	if err := r.db.WithContext(ctx).
		Where("destination_id = ? AND job_id = ?", destinationID, jobID).
		Delete(&db.DestinationLock{}).Error; err != nil {
		return fmt.Errorf("destinations: release busy: %w", err)
	}
	return nil
}

// ReleaseBusyForJobs releases every hold of a set of jobs, so a job that ends
// without reporting each destination can never leave one locked.
func (r *gormDestinationRepository) ReleaseBusyForJobs(ctx context.Context, jobIDs []uuid.UUID) error {
	if len(jobIDs) == 0 {
		return nil
	}
	if err := r.db.WithContext(ctx).
		Where("job_id IN ?", jobIDs).
		Delete(&db.DestinationLock{}).Error; err != nil {
		return fmt.Errorf("destinations: release busy for jobs: %w", err)
	}
	return nil
}

// ReleaseStaleBusy clears every hold of an inactive job (see lockHolderActive).
// Called once at server startup, after jobs left running by the previous
// process have been marked interrupted, so the busy state the GUI shows reflects
// only operations actually in flight. Returns the number of holds released.
func (r *gormDestinationRepository) ReleaseStaleBusy(ctx context.Context) (int64, error) {
	result := r.db.WithContext(ctx).
		Where("NOT " + lockHolderActive).
		Delete(&db.DestinationLock{})
	if result.Error != nil {
		return 0, fmt.Errorf("destinations: release stale busy: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// ListLocks returns the holds of active jobs on the given destinations, oldest
// first.
func (r *gormDestinationRepository) ListLocks(ctx context.Context, destinationIDs []uuid.UUID) ([]db.DestinationLock, error) {
	if len(destinationIDs) == 0 {
		return nil, nil
	}
	var locks []db.DestinationLock
	if err := r.db.WithContext(ctx).
		Where("destination_id IN ? AND "+lockHolderActive, destinationIDs).
		Order("acquired_at ASC, job_id ASC").
		Find(&locks).Error; err != nil {
		return nil, fmt.Errorf("destinations: list locks: %w", err)
	}
	return locks, nil
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
