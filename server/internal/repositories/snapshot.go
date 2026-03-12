package repositories

import (
	"context"
	"errors"
	"fmt"

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
			policies.name   AS policy_name,
			destinations.name AS destination_name`).
		Joins("LEFT JOIN policies ON policies.id = snapshots.policy_id").
		Joins("LEFT JOIN destinations ON destinations.id = snapshots.destination_id").
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

// DeleteBySnapshotID removes a snapshot record by the opaque engine snapshot ID.
// Used during retention policy enforcement when the backup engine prunes old
// snapshots — the cached records in the database must be kept in sync.
func (r *gormSnapshotRepository) DeleteBySnapshotID(ctx context.Context, snapshotID string) error {
	result := r.db.WithContext(ctx).
		Where("snapshot_id = ?", snapshotID).
		Delete(&db.Snapshot{})
	if result.Error != nil {
		return fmt.Errorf("snapshots: delete by snapshot id: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}