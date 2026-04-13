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

// ─── Delivery queue ───────────────────────────────────────────────────────────

// CreateDelivery inserts a new delivery row into notification_delivery_queue.
func (r *gormNotificationRepository) CreateDelivery(ctx context.Context, d *db.NotificationDelivery) error {
	if err := r.db.WithContext(ctx).Create(d).Error; err != nil {
		return fmt.Errorf("notifications: create delivery: %w", err)
	}
	return nil
}

// UpdateDelivery saves changes to an existing delivery row (typically status,
// attempts, last_error, next_retry_at after a send attempt).
func (r *gormNotificationRepository) UpdateDelivery(ctx context.Context, d *db.NotificationDelivery) error {
	if err := r.db.WithContext(ctx).Save(d).Error; err != nil {
		return fmt.Errorf("notifications: update delivery: %w", err)
	}
	return nil
}

// ListPendingDeliveries returns up to limit rows with status="pending" and
// next_retry_at at or before `before`. Rows with a NULL next_retry_at are
// always included (they are ready to be sent immediately).
func (r *gormNotificationRepository) ListPendingDeliveries(ctx context.Context, before time.Time, limit int) ([]*db.NotificationDelivery, error) {
	var rows []*db.NotificationDelivery
	if err := r.db.WithContext(ctx).
		Where("status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= ?)", before).
		Order("created_at ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("notifications: list pending deliveries: %w", err)
	}
	return rows, nil
}

// ListDeliveriesByStatus returns delivery rows filtered by status, newest first.
// Used by the admin queue visibility endpoint.
func (r *gormNotificationRepository) ListDeliveriesByStatus(ctx context.Context, status string, opts ListOptions) ([]db.NotificationDelivery, int64, error) {
	var rows []db.NotificationDelivery
	var total int64

	q := r.db.WithContext(ctx).Model(&db.NotificationDelivery{}).Where("status = ?", status)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("notifications: list deliveries count: %w", err)
	}
	if err := q.Order("created_at DESC").Limit(opts.Limit).Offset(opts.Offset).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("notifications: list deliveries: %w", err)
	}
	return rows, total, nil
}