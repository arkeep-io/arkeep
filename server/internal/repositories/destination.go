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