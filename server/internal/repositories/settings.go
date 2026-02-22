package repositories

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// gormSettingsRepository is the GORM-backed implementation of SettingsRepository.
type gormSettingsRepository struct {
	database *gorm.DB
}

// NewSettingsRepository creates a new SettingsRepository backed by GORM.
func NewSettingsRepository(database *gorm.DB) SettingsRepository {
	return &gormSettingsRepository{database: database}
}

// Get retrieves a single setting by its exact key.
func (r *gormSettingsRepository) Get(ctx context.Context, key string) (*db.Setting, error) {
	var s db.Setting
	err := r.database.WithContext(ctx).First(&s, "key = ?", key).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

// Set upserts a setting. On conflict (key already exists) the value and
// updated_at are overwritten. This avoids a read-before-write on every save.
func (r *gormSettingsRepository) Set(ctx context.Context, key string, value db.EncryptedString) error {
	s := db.Setting{Key: key, Value: value}
	return r.database.WithContext(ctx).
		Save(&s).Error
}

// GetMany retrieves all settings whose key starts with prefix.
// Useful for loading an entire config namespace (e.g. all "smtp.*" keys).
func (r *gormSettingsRepository) GetMany(ctx context.Context, prefix string) ([]db.Setting, error) {
	var settings []db.Setting
	err := r.database.WithContext(ctx).
		Where("key LIKE ?", prefix+"%").
		Find(&settings).Error
	if err != nil {
		return nil, err
	}
	return settings, nil
}

// Delete removes a setting by key. Silently succeeds if the key is absent
// (idempotent delete is the expected contract for configuration cleanup).
func (r *gormSettingsRepository) Delete(ctx context.Context, key string) error {
	return r.database.WithContext(ctx).
		Delete(&db.Setting{}, "key = ?", key).Error
}