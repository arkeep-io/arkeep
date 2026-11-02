package repositories

import (
	"context"
	"errors"
	"fmt"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type gormPasswordChangeChallengeRepository struct {
	db *gorm.DB
}

// NewPasswordChangeChallengeRepository returns a
// PasswordChangeChallengeRepository backed by the provided *gorm.DB.
func NewPasswordChangeChallengeRepository(db *gorm.DB) PasswordChangeChallengeRepository {
	return &gormPasswordChangeChallengeRepository{db: db}
}

func (r *gormPasswordChangeChallengeRepository) Create(ctx context.Context, challenge *db.PasswordChangeChallenge) error {
	if err := r.db.WithContext(ctx).Create(challenge).Error; err != nil {
		return fmt.Errorf("password_change_challenges: create: %w", err)
	}
	return nil
}

func (r *gormPasswordChangeChallengeRepository) GetUnusedByHash(ctx context.Context, hash string) (*db.PasswordChangeChallenge, error) {
	var challenge db.PasswordChangeChallenge
	err := r.db.WithContext(ctx).
		First(&challenge, "token_hash = ? AND used_at IS NULL", hash).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("password_change_challenges: get unused by hash: %w", err)
	}
	return &challenge, nil
}

// Consume is conditional on used_at being NULL, so two concurrent requests
// with the same token cannot both get past it.
func (r *gormPasswordChangeChallengeRepository) Consume(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Model(&db.PasswordChangeChallenge{}).
		Where("id = ? AND used_at IS NULL", id).
		Update("used_at", gorm.Expr("CURRENT_TIMESTAMP"))
	if result.Error != nil {
		return fmt.Errorf("password_change_challenges: consume: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

func (r *gormPasswordChangeChallengeRepository) DeleteByUserID(ctx context.Context, userID uuid.UUID) error {
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&db.PasswordChangeChallenge{}).Error
	if err != nil {
		return fmt.Errorf("password_change_challenges: delete by user id: %w", err)
	}
	return nil
}
