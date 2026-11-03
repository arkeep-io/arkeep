// Package housekeeping provides a background service that deletes rows the
// server no longer needs, so their tables do not grow without bound: refresh
// tokens past their expiry (they can never be redeemed again) and
// notifications read more than 30 days ago. Unread notifications are kept.
package housekeeping

import (
	"context"
	"time"

	"go.uber.org/zap"
)

const (
	// runInterval is how often the sweep runs.
	runInterval = 24 * time.Hour
	// readNotificationRetention is how long a read notification is kept.
	readNotificationRetention = 30 * 24 * time.Hour
)

// tokenStore is the subset of the refresh token repository the service needs.
type tokenStore interface {
	DeleteExpired(ctx context.Context, before time.Time) error
}

// notificationStore is the subset of the notification repository the service
// needs.
type notificationStore interface {
	DeleteReadOlderThan(ctx context.Context, t time.Time) error
}

// Service periodically deletes expired refresh tokens and old read
// notifications.
type Service struct {
	tokens        tokenStore
	notifications notificationStore
	logger        *zap.Logger
}

// NewService creates a housekeeping Service.
func NewService(tokens tokenStore, notifications notificationStore, logger *zap.Logger) *Service {
	return &Service{
		tokens:        tokens,
		notifications: notifications,
		logger:        logger.Named("housekeeping"),
	}
}

// Start runs a sweep at once, then every runInterval until ctx is cancelled.
func (s *Service) Start(ctx context.Context) {
	ticker := time.NewTicker(runInterval)
	defer ticker.Stop()

	s.RunOnce(ctx, time.Now().UTC())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.RunOnce(ctx, time.Now().UTC())
		}
	}
}

// RunOnce performs one sweep as of now. Errors are logged: a failed sweep is
// retried on the next run.
func (s *Service) RunOnce(ctx context.Context, now time.Time) {
	if err := s.tokens.DeleteExpired(ctx, now); err != nil {
		s.logger.Error("failed to delete expired refresh tokens", zap.Error(err))
	}
	if err := s.notifications.DeleteReadOlderThan(ctx, now.Add(-readNotificationRetention)); err != nil {
		s.logger.Error("failed to delete old read notifications", zap.Error(err))
	}
}
