package repositories

import (
	"context"
	"errors"
	"fmt"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// gormPasswordResetTokenRepository is the GORM implementation of
// PasswordResetTokenRepository.
type gormPasswordResetTokenRepository struct {
	db *gorm.DB
}

// NewPasswordResetTokenRepository returns a PasswordResetTokenRepository backed
// by the provided *gorm.DB.
func NewPasswordResetTokenRepository(db *gorm.DB) PasswordResetTokenRepository {
	return &gormPasswordResetTokenRepository{db: db}
}

// Create inserts a new password reset token record.
func (r *gormPasswordResetTokenRepository) Create(ctx context.Context, token *db.PasswordResetToken) error {
	if err := r.db.WithContext(ctx).Create(token).Error; err != nil {
		return fmt.Errorf("password_reset_tokens: create: %w", err)
	}
	return nil
}

// GetUnusedByHash retrieves an unused token by its SHA-256 hash. Expiry is left
// to the caller (checked in Go) to avoid fragile SQL timestamp comparisons.
// Returns ErrNotFound if no matching unused token exists.
func (r *gormPasswordResetTokenRepository) GetUnusedByHash(ctx context.Context, hash string) (*db.PasswordResetToken, error) {
	var token db.PasswordResetToken
	err := r.db.WithContext(ctx).
		First(&token, "token_hash = ? AND used_at IS NULL", hash).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("password_reset_tokens: get valid by hash: %w", err)
	}
	return &token, nil
}

// MarkUsed sets the UsedAt timestamp, consuming the token so it cannot be reused.
// Returns ErrNotFound if no record matches the id.
func (r *gormPasswordResetTokenRepository) MarkUsed(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Model(&db.PasswordResetToken{}).
		Where("id = ?", id).
		Update("used_at", gorm.Expr("CURRENT_TIMESTAMP"))
	if result.Error != nil {
		return fmt.Errorf("password_reset_tokens: mark used: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteByUserID permanently removes all reset tokens for a user. Used when a
// new reset is requested so any previously issued links stop working.
func (r *gormPasswordResetTokenRepository) DeleteByUserID(ctx context.Context, userID uuid.UUID) error {
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&db.PasswordResetToken{}).Error
	if err != nil {
		return fmt.Errorf("password_reset_tokens: delete by user id: %w", err)
	}
	return nil
}
