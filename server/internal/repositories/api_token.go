package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// gormAPITokenRepository is the GORM implementation of APITokenRepository.
type gormAPITokenRepository struct {
	db *gorm.DB
}

// NewAPITokenRepository returns an APITokenRepository backed by the provided
// *gorm.DB.
func NewAPITokenRepository(db *gorm.DB) APITokenRepository {
	return &gormAPITokenRepository{db: db}
}

// Create inserts a new API token.
func (r *gormAPITokenRepository) Create(ctx context.Context, token *db.APIToken) error {
	if err := r.db.WithContext(ctx).Create(token).Error; err != nil {
		return fmt.Errorf("api_tokens: create: %w", err)
	}
	return nil
}

// GetByHash returns the token whose hash matches.
func (r *gormAPITokenRepository) GetByHash(ctx context.Context, hash string) (*db.APIToken, error) {
	var token db.APIToken
	err := r.db.WithContext(ctx).First(&token, "token_hash = ?", hash).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("api_tokens: get by hash: %w", err)
	}
	return &token, nil
}

// ListByUser returns a user's tokens, newest first.
func (r *gormAPITokenRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]db.APIToken, error) {
	var tokens []db.APIToken
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC, id DESC").
		Find(&tokens).Error
	if err != nil {
		return nil, fmt.Errorf("api_tokens: list by user: %w", err)
	}
	return tokens, nil
}

// CountByUser returns how many tokens a user has.
func (r *gormAPITokenRepository) CountByUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&db.APIToken{}).Where("user_id = ?", userID).Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("api_tokens: count by user: %w", err)
	}
	return n, nil
}

// ListAll returns every token with its owner's email, newest first.
func (r *gormAPITokenRepository) ListAll(ctx context.Context) ([]APITokenWithOwner, error) {
	var rows []APITokenWithOwner
	err := r.db.WithContext(ctx).
		Table("api_tokens").
		Select("api_tokens.*, users.email AS user_email").
		Joins("JOIN users ON users.id = api_tokens.user_id").
		Order("api_tokens.created_at DESC, api_tokens.id DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("api_tokens: list all: %w", err)
	}
	return rows, nil
}

// Delete removes a token, only if userID owns it when userID is not nil.
func (r *gormAPITokenRepository) Delete(ctx context.Context, id uuid.UUID, userID *uuid.UUID) (*db.APIToken, error) {
	var token db.APIToken
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := tx.Where("id = ?", id)
		if userID != nil {
			q = q.Where("user_id = ?", *userID)
		}
		if err := q.First(&token).Error; err != nil {
			return err
		}
		return tx.Delete(&db.APIToken{}, "id = ?", token.ID).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("api_tokens: delete: %w", err)
	}
	return &token, nil
}

// DeleteByUserID removes every token of a user.
func (r *gormAPITokenRepository) DeleteByUserID(ctx context.Context, userID uuid.UUID) error {
	if err := r.db.WithContext(ctx).Delete(&db.APIToken{}, "user_id = ?", userID).Error; err != nil {
		return fmt.Errorf("api_tokens: delete by user: %w", err)
	}
	return nil
}

// TouchLastUsed records that the token was used at t. UpdatedAt is left
// alone: it records changes to the token, not its use.
func (r *gormAPITokenRepository) TouchLastUsed(ctx context.Context, id uuid.UUID, t time.Time) error {
	err := r.db.WithContext(ctx).
		Model(&db.APIToken{}).
		Where("id = ?", id).
		UpdateColumn("last_used_at", t.UTC()).Error
	if err != nil {
		return fmt.Errorf("api_tokens: touch last used: %w", err)
	}
	return nil
}
