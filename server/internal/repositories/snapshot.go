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

// SnapshotWithNames extends db.Snapshot with denormalised display names
// resolved via JOIN. Used by list endpoints so the GUI does not need
// separate requests to resolve policy and destination names.
type SnapshotWithNames struct {
	db.Snapshot
	PolicyName      string
	DestinationName string
	AgentID         string
	AgentName       string
}

// gormSnapshotRepository is the GORM implementation of SnapshotRepository.
type gormSnapshotRepository struct {
	db *gorm.DB
}

// NewSnapshotRepository returns a SnapshotRepository backed by the provided *gorm.DB.
func NewSnapshotRepository(db *gorm.DB) SnapshotRepository {
	return &gormSnapshotRepository{db: db}
}

// Create inserts a new snapshot record into the database.
// Snapshots are created after each successful backup job and represent
// a point-in-time state of the backed-up data cached from the backup engine.
func (r *gormSnapshotRepository) Create(ctx context.Context, snapshot *db.Snapshot) error {
	if err := r.db.WithContext(ctx).Create(snapshot).Error; err != nil {
		return fmt.Errorf("snapshots: create: %w", err)
	}
	return nil
}

// GetByID retrieves a snapshot by its UUID.
// Returns ErrNotFound if no record exists.
func (r *gormSnapshotRepository) GetByID(ctx context.Context, id uuid.UUID) (*db.Snapshot, error) {
	var snapshot db.Snapshot
	err := r.db.WithContext(ctx).First(&snapshot, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("snapshots: get by id: %w", err)
	}
	return &snapshot, nil
}

// Delete permanently removes a snapshot record by ID.
// Note: this only removes the cached record from the database — the actual
// snapshot in the backup engine must be deleted separately via the backup
// engine's forget/prune commands.
func (r *gormSnapshotRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&db.Snapshot{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("snapshots: delete: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// listWithNamesQuery returns a base query that JOINs policies and destinations
// to resolve display names. The SELECT clause maps the joined columns to the
// SnapshotWithNames fields. All list methods share this base.
func (r *gormSnapshotRepository) listWithNamesQuery(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).
		Table("snapshots").
		Select(`snapshots.*,
			policies.name        AS policy_name,
			destinations.name    AS destination_name,
			policies.agent_id    AS agent_id,
			agents.name          AS agent_name`).
		Joins("LEFT JOIN policies ON policies.id = snapshots.policy_id").
		Joins("LEFT JOIN destinations ON destinations.id = snapshots.destination_id").
		Joins("LEFT JOIN agents ON agents.id = policies.agent_id").
		Order("snapshots.snapshot_at DESC")
}

// List returns a paginated list of snapshots with resolved names and the total count.
func (r *gormSnapshotRepository) List(ctx context.Context, opts ListOptions) ([]SnapshotWithNames, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).Model(&db.Snapshot{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("snapshots: list count: %w", err)
	}

	var rows []SnapshotWithNames
	if err := r.listWithNamesQuery(ctx).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("snapshots: list: %w", err)
	}
	return rows, total, nil
}

// ListByPolicy returns a paginated list of snapshots for a given policy with resolved names.
func (r *gormSnapshotRepository) ListByPolicy(ctx context.Context, policyID uuid.UUID, opts ListOptions) ([]SnapshotWithNames, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).
		Model(&db.Snapshot{}).
		Where("policy_id = ?", policyID).
		Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("snapshots: list by policy count: %w", err)
	}

	var rows []SnapshotWithNames
	if err := r.listWithNamesQuery(ctx).
		Where("snapshots.policy_id = ?", policyID).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("snapshots: list by policy: %w", err)
	}
	return rows, total, nil
}

// ListByDestination returns a paginated list of snapshots for a given destination with resolved names.
func (r *gormSnapshotRepository) ListByDestination(ctx context.Context, destinationID uuid.UUID, opts ListOptions) ([]SnapshotWithNames, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).
		Model(&db.Snapshot{}).
		Where("destination_id = ?", destinationID).
		Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("snapshots: list by destination count: %w", err)
	}

	var rows []SnapshotWithNames
	if err := r.listWithNamesQuery(ctx).
		Where("snapshots.destination_id = ?", destinationID).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("snapshots: list by destination: %w", err)
	}
	return rows, total, nil
}

// ExistsBySnapshotIDAndDestination returns true if a snapshot with the given
// opaque snapshot ID already exists for the specified destination.
// Used to avoid creating duplicate records during bulk import.
func (r *gormSnapshotRepository) ExistsBySnapshotIDAndDestination(ctx context.Context, snapshotID string, destinationID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&db.Snapshot{}).
		Where("snapshot_id = ? AND destination_id = ?", snapshotID, destinationID).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("snapshots: exists by snapshot id: %w", err)
	}
	return count > 0, nil
}

// DeleteStaleByDestination removes cached snapshot records for a destination
// whose opaque engine snapshot ID is absent from liveIDs — that is, snapshots
// the backup engine has pruned. This is how the database learns about retention
// enforcement: the engine prunes the repository and the cached records must be
// kept in sync.
//
// liveIDs must come from an UNFILTERED repository listing. A destination maps
// 1:1 to a repository, so an unfiltered listing is authoritative for every row
// carrying that destination_id; a filtered one would make this delete valid
// records belonging to other policies.
//
// Only records with snapshot_at strictly before cutoff are considered, so a
// snapshot created after the listing was taken — for instance by a concurrent
// backup from another agent writing to the same repository — is never removed.
//
// Returns the number of records removed. Callers must reject an empty liveIDs
// slice before calling: an empty listing means the engine could not be read,
// not that the repository is empty.
func (r *gormSnapshotRepository) DeleteStaleByDestination(
	ctx context.Context, destinationID uuid.UUID, liveIDs []string, cutoff time.Time,
) (int64, error) {
	result := r.db.WithContext(ctx).
		Where("destination_id = ? AND snapshot_at < ? AND snapshot_id NOT IN ?",
			destinationID, cutoff, liveIDs).
		Delete(&db.Snapshot{})
	if result.Error != nil {
		return 0, fmt.Errorf("snapshots: delete stale by destination: %w", result.Error)
	}
	return result.RowsAffected, nil
}