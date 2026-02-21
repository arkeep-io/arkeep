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

// gormNotificationRepository is the GORM implementation of NotificationRepository.
type gormNotificationRepository struct {
	db *gorm.DB
}

// NewNotificationRepository returns a NotificationRepository backed by the provided *gorm.DB.
func NewNotificationRepository(db *gorm.DB) NotificationRepository {
	return &gormNotificationRepository{db: db}
}

// Create inserts a new notification record into the database.
// After insertion, the caller is responsible for broadcasting the notification
// to the user via the WebSocket hub.
func (r *gormNotificationRepository) Create(ctx context.Context, notification *db.Notification) error {
	if err := r.db.WithContext(ctx).Create(notification).Error; err != nil {
		return fmt.Errorf("notifications: create: %w", err)
	}
	return nil
}

// GetByID retrieves a notification by its UUID.
// Returns ErrNotFound if no record exists.
func (r *gormNotificationRepository) GetByID(ctx context.Context, id uuid.UUID) (*db.Notification, error) {
	var notification db.Notification
	err := r.db.WithContext(ctx).First(&notification, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("notifications: get by id: %w", err)
	}
	return &notification, nil
}

// MarkAsRead sets the read_at timestamp on a single notification.
// Returns ErrNotFound if no record exists or it is already read.
func (r *gormNotificationRepository) MarkAsRead(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Model(&db.Notification{}).
		Where("id = ? AND read_at IS NULL", id).
		Update("read_at", time.Now())
	if result.Error != nil {
		return fmt.Errorf("notifications: mark as read: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkAllAsRead sets read_at on all unread notifications for a given user.
// Used when the user clicks "mark all as read" in the GUI.
func (r *gormNotificationRepository) MarkAllAsRead(ctx context.Context, userID uuid.UUID) error {
	if err := r.db.WithContext(ctx).
		Model(&db.Notification{}).
		Where("user_id = ? AND read_at IS NULL", userID).
		Update("read_at", time.Now()).Error; err != nil {
		return fmt.Errorf("notifications: mark all as read: %w", err)
	}
	return nil
}

// Delete permanently removes a notification record by ID.
// Returns ErrNotFound if no record exists.
func (r *gormNotificationRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&db.Notification{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("notifications: delete: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListByUser returns a paginated list of notifications for a given user,
// ordered by creation time descending (most recent first).
// Unread notifications are returned regardless of age. Read notifications
// older than 30 days are purged by DeleteReadOlderThan.
func (r *gormNotificationRepository) ListByUser(ctx context.Context, userID uuid.UUID, opts ListOptions) ([]db.Notification, int64, error) {
	var notifications []db.Notification
	var total int64

	if err := r.db.WithContext(ctx).
		Model(&db.Notification{}).
		Where("user_id = ?", userID).
		Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("notifications: list by user count: %w", err)
	}

	if err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Limit(opts.Limit).
		Offset(opts.Offset).
		Order("created_at DESC").
		Find(&notifications).Error; err != nil {
		return nil, 0, fmt.Errorf("notifications: list by user: %w", err)
	}

	return notifications, total, nil
}

// DeleteReadOlderThan permanently removes read notifications older than the
// given time. Intended to be called periodically by a background cleanup job
// to prevent unbounded growth of the notifications table.
//
// Example — purge notifications read more than 30 days ago:
//
//	repo.DeleteReadOlderThan(ctx, time.Now().AddDate(0, 0, -30))
func (r *gormNotificationRepository) DeleteReadOlderThan(ctx context.Context, t time.Time) error {
	if err := r.db.WithContext(ctx).
		Where("read_at IS NOT NULL AND read_at < ?", t).
		Delete(&db.Notification{}).Error; err != nil {
		return fmt.Errorf("notifications: delete read older than: %w", err)
	}
	return nil
}