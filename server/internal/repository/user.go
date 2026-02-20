package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// gormUserRepository is the GORM implementation of UserRepository.
type gormUserRepository struct {
	db *gorm.DB
}

// NewUserRepository returns a UserRepository backed by the provided *gorm.DB.
func NewUserRepository(db *gorm.DB) UserRepository {
	return &gormUserRepository{db: db}
}

// Create inserts a new user record into the database.
func (r *gormUserRepository) Create(ctx context.Context, user *db.User) error {
	if err := r.db.WithContext(ctx).Create(user).Error; err != nil {
		return fmt.Errorf("users: create: %w", err)
	}
	return nil
}

// GetByID retrieves a user by its UUID. Returns ErrNotFound if no record exists.
func (r *gormUserRepository) GetByID(ctx context.Context, id uuid.UUID) (*db.User, error) {
	var user db.User
	err := r.db.WithContext(ctx).First(&user, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("users: get by id: %w", err)
	}
	return &user, nil
}

// GetByEmail retrieves a user by email address. Returns ErrNotFound if no record exists.
func (r *gormUserRepository) GetByEmail(ctx context.Context, email string) (*db.User, error) {
	var user db.User
	err := r.db.WithContext(ctx).First(&user, "email = ?", email).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("users: get by email: %w", err)
	}
	return &user, nil
}

// GetByOIDC retrieves a user by OIDC provider ID and subject claim.
// Returns ErrNotFound if no record exists.
func (r *gormUserRepository) GetByOIDC(ctx context.Context, provider, sub string) (*db.User, error) {
	var user db.User
	err := r.db.WithContext(ctx).
		First(&user, "oidc_provider = ? AND oidc_sub = ?", provider, sub).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("users: get by oidc: %w", err)
	}
	return &user, nil
}

// Update persists changes to an existing user record.
// Only non-zero fields are updated — use Save instead of Updates if you need
// to explicitly zero out a field.
func (r *gormUserRepository) Update(ctx context.Context, user *db.User) error {
	result := r.db.WithContext(ctx).Save(user)
	if result.Error != nil {
		return fmt.Errorf("users: update: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete permanently removes a user record by ID.
func (r *gormUserRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&db.User{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("users: delete: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns a paginated list of users and the total count.
// Use ListOptions.Limit and ListOptions.Offset for pagination.
func (r *gormUserRepository) List(ctx context.Context, opts ListOptions) ([]db.User, int64, error) {
	var users []db.User
	var total int64

	if err := r.db.WithContext(ctx).Model(&db.User{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("users: list count: %w", err)
	}

	if err := r.db.WithContext(ctx).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Order("created_at ASC").
		Find(&users).Error; err != nil {
		return nil, 0, fmt.Errorf("users: list: %w", err)
	}

	return users, total, nil
}

// -----------------------------------------------------------------------------
// gormRefreshTokenRepository
// -----------------------------------------------------------------------------

// gormRefreshTokenRepository is the GORM implementation of RefreshTokenRepository.
type gormRefreshTokenRepository struct {
	db *gorm.DB
}

// NewRefreshTokenRepository returns a RefreshTokenRepository backed by the provided *gorm.DB.
func NewRefreshTokenRepository(db *gorm.DB) RefreshTokenRepository {
	return &gormRefreshTokenRepository{db: db}
}

// Create inserts a new refresh token record.
func (r *gormRefreshTokenRepository) Create(ctx context.Context, token *db.RefreshToken) error {
	if err := r.db.WithContext(ctx).Create(token).Error; err != nil {
		return fmt.Errorf("refresh_tokens: create: %w", err)
	}
	return nil
}

// GetByHash retrieves a refresh token by its SHA-256 hash.
// Returns ErrNotFound if no record exists.
func (r *gormRefreshTokenRepository) GetByHash(ctx context.Context, hash string) (*db.RefreshToken, error) {
	var token db.RefreshToken
	err := r.db.WithContext(ctx).First(&token, "token_hash = ?", hash).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("refresh_tokens: get by hash: %w", err)
	}
	return &token, nil
}

// Revoke sets the RevokedAt timestamp on a refresh token, invalidating it.
// Returns ErrNotFound if no record exists.
func (r *gormRefreshTokenRepository) Revoke(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Model(&db.RefreshToken{}).
		Where("id = ?", id).
		Update("revoked_at", gorm.Expr("CURRENT_TIMESTAMP"))
	if result.Error != nil {
		return fmt.Errorf("refresh_tokens: revoke: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeAllForUser revokes all active refresh tokens for a given user.
// Used on logout, password change, or security events.
func (r *gormRefreshTokenRepository) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	err := r.db.WithContext(ctx).
		Model(&db.RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", gorm.Expr("CURRENT_TIMESTAMP")).Error
	if err != nil {
		return fmt.Errorf("refresh_tokens: revoke all for user: %w", err)
	}
	return nil
}

// DeleteExpired permanently removes all expired refresh tokens from the database.
// Intended to be called periodically by a background cleanup job.
func (r *gormRefreshTokenRepository) DeleteExpired(ctx context.Context) error {
	err := r.db.WithContext(ctx).
		Where("expires_at < CURRENT_TIMESTAMP").
		Delete(&db.RefreshToken{}).Error
	if err != nil {
		return fmt.Errorf("refresh_tokens: delete expired: %w", err)
	}
	return nil
}

// DeleteByHash permanently removes a refresh token by its SHA-256 hash.
// If no token matches the hash the call is a no-op — the desired state
// (token gone) is already met.
func (r *gormRefreshTokenRepository) DeleteByHash(ctx context.Context, hash string) error {
	err := r.db.WithContext(ctx).
		Where("token_hash = ?", hash).
		Delete(&db.RefreshToken{}).Error
	if err != nil {
		return fmt.Errorf("refresh_tokens: delete by hash: %w", err)
	}
	return nil
}