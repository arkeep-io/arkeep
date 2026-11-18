package repositories

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// gormNotificationProviderRepository is the GORM implementation of
// NotificationProviderRepository.
type gormNotificationProviderRepository struct {
	db *gorm.DB
}

// NewNotificationProviderRepository returns a NotificationProviderRepository
// backed by the provided *gorm.DB.
func NewNotificationProviderRepository(db *gorm.DB) NotificationProviderRepository {
	return &gormNotificationProviderRepository{db: db}
}

// Create inserts a new notification provider. Secrets is encrypted by
// EncryptedString.Value().
func (r *gormNotificationProviderRepository) Create(ctx context.Context, p *db.NotificationProvider) error {
	if err := r.db.WithContext(ctx).Create(p).Error; err != nil {
		return fmt.Errorf("notification_providers: create: %w", err)
	}
	return nil
}

// GetByID retrieves a notification provider by ID. Returns ErrNotFound if no
// record exists.
func (r *gormNotificationProviderRepository) GetByID(ctx context.Context, id uuid.UUID) (*db.NotificationProvider, error) {
	var p db.NotificationProvider
	if err := r.db.WithContext(ctx).First(&p, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("notification_providers: get by id: %w", err)
	}
	return &p, nil
}

// List retrieves every notification provider ordered by creation time.
func (r *gormNotificationProviderRepository) List(ctx context.Context) ([]*db.NotificationProvider, error) {
	var providers []*db.NotificationProvider
	if err := r.db.WithContext(ctx).Order("created_at ASC").Find(&providers).Error; err != nil {
		return nil, fmt.Errorf("notification_providers: list: %w", err)
	}
	return providers, nil
}

// ListEnabled retrieves the enabled notification providers ordered by
// creation time.
func (r *gormNotificationProviderRepository) ListEnabled(ctx context.Context) ([]*db.NotificationProvider, error) {
	var providers []*db.NotificationProvider
	if err := r.db.WithContext(ctx).Where("enabled = ?", true).Order("created_at ASC").Find(&providers).Error; err != nil {
		return nil, fmt.Errorf("notification_providers: list enabled: %w", err)
	}
	return providers, nil
}

// Update persists every field of an existing notification provider.
func (r *gormNotificationProviderRepository) Update(ctx context.Context, p *db.NotificationProvider) error {
	result := r.db.WithContext(ctx).Save(p)
	if result.Error != nil {
		return fmt.Errorf("notification_providers: update: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete permanently removes a notification provider. Its pending deliveries
// are removed with it (ON DELETE CASCADE).
func (r *gormNotificationProviderRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&db.NotificationProvider{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("notification_providers: delete: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
