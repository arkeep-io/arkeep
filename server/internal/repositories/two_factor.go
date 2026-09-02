package repositories

import (
	"context"
	"errors"
	"fmt"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// -----------------------------------------------------------------------------
// TwoFactorChallengeRepository
// -----------------------------------------------------------------------------

type gormTwoFactorChallengeRepository struct {
	db *gorm.DB
}

// NewTwoFactorChallengeRepository returns a TwoFactorChallengeRepository backed
// by the provided *gorm.DB.
func NewTwoFactorChallengeRepository(db *gorm.DB) TwoFactorChallengeRepository {
	return &gormTwoFactorChallengeRepository{db: db}
}

func (r *gormTwoFactorChallengeRepository) Create(ctx context.Context, challenge *db.TwoFactorChallenge) error {
	if err := r.db.WithContext(ctx).Create(challenge).Error; err != nil {
		return fmt.Errorf("two_factor_challenges: create: %w", err)
	}
	return nil
}

func (r *gormTwoFactorChallengeRepository) GetUnusedByHash(ctx context.Context, hash string) (*db.TwoFactorChallenge, error) {
	var challenge db.TwoFactorChallenge
	err := r.db.WithContext(ctx).
		First(&challenge, "token_hash = ? AND used_at IS NULL", hash).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("two_factor_challenges: get unused by hash: %w", err)
	}
	return &challenge, nil
}

func (r *gormTwoFactorChallengeRepository) IncrementAttempts(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Model(&db.TwoFactorChallenge{}).
		Where("id = ?", id).
		Update("attempts", gorm.Expr("attempts + 1"))
	if result.Error != nil {
		return fmt.Errorf("two_factor_challenges: increment attempts: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *gormTwoFactorChallengeRepository) MarkUsed(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Model(&db.TwoFactorChallenge{}).
		Where("id = ?", id).
		Update("used_at", gorm.Expr("CURRENT_TIMESTAMP"))
	if result.Error != nil {
		return fmt.Errorf("two_factor_challenges: mark used: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *gormTwoFactorChallengeRepository) DeleteByUserID(ctx context.Context, userID uuid.UUID) error {
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&db.TwoFactorChallenge{}).Error
	if err != nil {
		return fmt.Errorf("two_factor_challenges: delete by user id: %w", err)
	}
	return nil
}

// -----------------------------------------------------------------------------
// RecoveryCodeRepository
// -----------------------------------------------------------------------------

type gormRecoveryCodeRepository struct {
	db *gorm.DB
}

// NewRecoveryCodeRepository returns a RecoveryCodeRepository backed by the
// provided *gorm.DB.
func NewRecoveryCodeRepository(db *gorm.DB) RecoveryCodeRepository {
	return &gormRecoveryCodeRepository{db: db}
}

func (r *gormRecoveryCodeRepository) CreateBatch(ctx context.Context, codes []db.RecoveryCode) error {
	if len(codes) == 0 {
		return nil
	}
	if err := r.db.WithContext(ctx).Create(&codes).Error; err != nil {
		return fmt.Errorf("recovery_codes: create batch: %w", err)
	}
	return nil
}

func (r *gormRecoveryCodeRepository) GetUnusedByHash(ctx context.Context, hash string) (*db.RecoveryCode, error) {
	var code db.RecoveryCode
	err := r.db.WithContext(ctx).
		First(&code, "code_hash = ? AND used_at IS NULL", hash).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("recovery_codes: get unused by hash: %w", err)
	}
	return &code, nil
}

func (r *gormRecoveryCodeRepository) MarkUsed(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Model(&db.RecoveryCode{}).
		Where("id = ?", id).
		Update("used_at", gorm.Expr("CURRENT_TIMESTAMP"))
	if result.Error != nil {
		return fmt.Errorf("recovery_codes: mark used: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *gormRecoveryCodeRepository) CountUnused(ctx context.Context, userID uuid.UUID) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&db.RecoveryCode{}).
		Where("user_id = ? AND used_at IS NULL", userID).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("recovery_codes: count unused: %w", err)
	}
	return count, nil
}

func (r *gormRecoveryCodeRepository) DeleteByUserID(ctx context.Context, userID uuid.UUID) error {
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&db.RecoveryCode{}).Error
	if err != nil {
		return fmt.Errorf("recovery_codes: delete by user id: %w", err)
	}
	return nil
}
