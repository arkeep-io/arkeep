package repositories

import (
	"context"
	"errors"
	"fmt"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// gormOIDCProviderRepository is the GORM implementation of OIDCProviderRepository.
type gormOIDCProviderRepository struct {
	db *gorm.DB
}

// NewOIDCProviderRepository returns an OIDCProviderRepository backed by the provided *gorm.DB.
func NewOIDCProviderRepository(db *gorm.DB) OIDCProviderRepository {
	return &gormOIDCProviderRepository{db: db}
}

// Create inserts a new OIDC provider record into the database.
// ClientSecret is automatically encrypted by EncryptedString.Value().
func (r *gormOIDCProviderRepository) Create(ctx context.Context, provider *db.OIDCProvider) error {
	if err := r.db.WithContext(ctx).Create(provider).Error; err != nil {
		return fmt.Errorf("oidc_providers: create: %w", err)
	}
	return nil
}

// GetByID retrieves an OIDC provider by its UUID.
// Returns ErrNotFound if no record exists.
func (r *gormOIDCProviderRepository) GetByID(ctx context.Context, id uuid.UUID) (*db.OIDCProvider, error) {
	var provider db.OIDCProvider
	err := r.db.WithContext(ctx).First(&provider, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("oidc_providers: get by id: %w", err)
	}
	return &provider, nil
}

// List retrieves all OIDC providers ordered by creation time.
func (r *gormOIDCProviderRepository) List(ctx context.Context) ([]*db.OIDCProvider, error) {
	var providers []*db.OIDCProvider
	if err := r.db.WithContext(ctx).Order("created_at ASC").Find(&providers).Error; err != nil {
		return nil, fmt.Errorf("oidc_providers: list: %w", err)
	}
	return providers, nil
}

// ListEnabled retrieves all enabled OIDC providers ordered by creation time.
func (r *gormOIDCProviderRepository) ListEnabled(ctx context.Context) ([]*db.OIDCProvider, error) {
	var providers []*db.OIDCProvider
	if err := r.db.WithContext(ctx).Where("enabled = ?", true).Order("created_at ASC").Find(&providers).Error; err != nil {
		return nil, fmt.Errorf("oidc_providers: list enabled: %w", err)
	}
	return providers, nil
}

// Update persists all fields of an existing OIDC provider record.
// ClientSecret is automatically re-encrypted by EncryptedString.Value().
func (r *gormOIDCProviderRepository) Update(ctx context.Context, provider *db.OIDCProvider) error {
	result := r.db.WithContext(ctx).Save(provider)
	if result.Error != nil {
		return fmt.Errorf("oidc_providers: update: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete permanently removes an OIDC provider record by ID.
func (r *gormOIDCProviderRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&db.OIDCProvider{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("oidc_providers: delete: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
