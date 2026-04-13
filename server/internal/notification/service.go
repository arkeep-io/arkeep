package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
)

// Service is the single entry point for creating and delivering notifications.
// It persists in-app notifications to the database, publishes them to the
// WebSocket Hub, and fans out to external channels (email, webhook).
//
// Callers (scheduler, gRPC handlers, etc.) should use the typed methods
// (NotifyJobSucceeded, NotifyJobFailed, NotifyAgentOffline) rather than
// constructing events manually, so that notification content stays consistent
// across the codebase.
type Service interface {
	// NotifyJobSucceeded creates a success notification for the given job.
	// policyName is included in the message body for human readability.
	NotifyJobSucceeded(ctx context.Context, jobID, policyID uuid.UUID, policyName string) error

	// NotifyJobFailed creates a failure notification for the given job.
	// errMsg is the error string from the backup engine, included in the body.
	NotifyJobFailed(ctx context.Context, jobID, policyID uuid.UUID, policyName, errMsg string) error

	// NotifyAgentOffline creates a notification when an agent stops sending
	// heartbeats and is marked offline by the agent manager.
	NotifyAgentOffline(ctx context.Context, agentID uuid.UUID, agentName string) error
}

// NotificationService is the concrete implementation of Service.
// It is exported so that main.go can call Start(ctx) to launch the retrier.
type NotificationService struct {
	notifRepo    repositories.NotificationRepository
	userRepo     repositories.UserRepository
	settingsRepo repositories.SettingsRepository
	hub          *websocket.Hub
	email        *emailSender
	webhook      *webhookSender
	logger       *zap.Logger
}

// Config holds the dependencies required to build a notification Service.
type Config struct {
	NotifRepo    repositories.NotificationRepository
	UserRepo     repositories.UserRepository
	SettingsRepo repositories.SettingsRepository
	Hub          *websocket.Hub
	Logger       *zap.Logger
}

// NewService creates a new NotificationService. The email and webhook senders
// are wired internally — callers only need to provide the Config dependencies.
//
// The returned *NotificationService satisfies the Service interface. To start
// the background delivery retrier, call Start(ctx) after creation.
func NewService(cfg Config) *NotificationService {
	svc := &NotificationService{
		notifRepo:    cfg.NotifRepo,
		userRepo:     cfg.UserRepo,
		settingsRepo: cfg.SettingsRepo,
		hub:          cfg.Hub,
		logger:       cfg.Logger.Named("notification"),
	}

	// Wire senders with config loaders bound to this service's settings repo.
	// Config is reloaded on every send — no restart needed after settings change.
	svc.email = newEmailSender(func(ctx context.Context) (*SMTPConfig, error) {
		return loadSMTPConfig(ctx, cfg.SettingsRepo)
	})
	svc.webhook = newWebhookSender(func(ctx context.Context) (*WebhookConfig, error) {
		return loadWebhookConfig(ctx, cfg.SettingsRepo)
	})

	return svc
}

// Start launches the background delivery retrier. It runs until ctx is
// cancelled (i.e. server shutdown). Call it as a goroutine:
//
//	go notifSvc.Start(ctx)
//
// The retrier polls every 30 seconds for pending deliveries whose
// next_retry_at is in the past and attempts to resend them.
func (s *NotificationService) Start(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.processDeliveries(ctx)
		}
	}
}

// -----------------------------------------------------------------------------
// Public typed methods
// -----------------------------------------------------------------------------

func (s *NotificationService) NotifyJobSucceeded(ctx context.Context, jobID, policyID uuid.UUID, policyName string) error {
	payload := map[string]any{
		"job_id":      jobID.String(),
		"policy_id":   policyID.String(),
		"policy_name": policyName,
	}
	return s.notify(ctx, event{
		notifType: "job_success",
		title:     fmt.Sprintf("Backup completed: %s", policyName),
		body:      fmt.Sprintf("Policy \"%s\" completed successfully at %s.", policyName, time.Now().UTC().Format(time.RFC3339)),
		payload:   payload,
	})
}

func (s *NotificationService) NotifyJobFailed(ctx context.Context, jobID, policyID uuid.UUID, policyName, errMsg string) error {
	payload := map[string]any{
		"job_id":      jobID.String(),
		"policy_id":   policyID.String(),
		"policy_name": policyName,
		"error":       errMsg,
	}
	return s.notify(ctx, event{
		notifType: "job_failure",
		title:     fmt.Sprintf("Backup failed: %s", policyName),
		body:      fmt.Sprintf("Policy \"%s\" failed at %s: %s", policyName, time.Now().UTC().Format(time.RFC3339), errMsg),
		payload:   payload,
	})
}

func (s *NotificationService) NotifyAgentOffline(ctx context.Context, agentID uuid.UUID, agentName string) error {
	payload := map[string]any{
		"agent_id":   agentID.String(),
		"agent_name": agentName,
	}
	return s.notify(ctx, event{
		notifType: "agent_offline",
		title:     fmt.Sprintf("Agent offline: %s", agentName),
		body:      fmt.Sprintf("Agent \"%s\" stopped responding at %s.", agentName, time.Now().UTC().Format(time.RFC3339)),
		payload:   payload,
	})
}

// -----------------------------------------------------------------------------
// Internal event dispatch
// -----------------------------------------------------------------------------

// event carries the data for a single notification before it is fanned out
// to recipients and delivery channels.
type event struct {
	notifType string
	title     string
	body      string
	payload   map[string]any
}

// notify is the internal dispatch method. It:
//  1. Resolves the list of admin users as recipients.
//  2. Persists one db.Notification per recipient and publishes to the WebSocket Hub.
//  3. Enqueues one email delivery row and one webhook delivery row (both tied to
//     the first notification created) and attempts an optimistic send for each.
//     On failure the row stays pending and will be retried by the background
//     retrier (Start).
func (s *NotificationService) notify(ctx context.Context, ev event) error {
	admins, _, err := s.userRepo.List(ctx, repositories.ListOptions{Limit: 100, Offset: 0})
	if err != nil {
		return fmt.Errorf("notification: failed to list users: %w", err)
	}

	payloadJSON, err := json.Marshal(ev.payload)
	if err != nil {
		return fmt.Errorf("notification: failed to marshal payload: %w", err)
	}

	var emailRecipients []string
	// firstNotif is the first successfully-created db.Notification. Both the
	// email and webhook delivery rows reference its ID as the FK so that
	// retries can reload the notification content without extra storage.
	var firstNotif *db.Notification

	for i := range admins {
		u := &admins[i]
		if u.Role != "admin" || !u.IsActive {
			continue
		}

		n := &db.Notification{
			UserID:  u.ID,
			Type:    ev.notifType,
			Title:   ev.title,
			Body:    ev.body,
			Payload: string(payloadJSON),
		}
		if err := s.notifRepo.Create(ctx, n); err != nil {
			s.logger.Error("failed to persist notification",
				zap.String("user_id", u.ID.String()),
				zap.String("type", ev.notifType),
				zap.Error(err),
			)
			continue
		}

		if firstNotif == nil {
			firstNotif = n
		}

		topic := fmt.Sprintf("notifications:%s", u.ID.String())
		s.hub.Publish(topic, websocket.Message{
			Type:  websocket.MsgNotification,
			Topic: topic,
			Payload: map[string]any{
				"id":         n.ID.String(),
				"type":       n.Type,
				"title":      n.Title,
				"body":       n.Body,
				"payload":    ev.payload,
				"created_at": n.CreatedAt.UTC().Format(time.RFC3339),
			},
		})

		emailRecipients = append(emailRecipients, u.Email)
	}

	// External deliveries are per-event (not per-admin). Both rows are tied to
	// firstNotif.ID so the retrier can reload title/body/payload on retry.
	// Skip if no notifications were persisted (e.g. no active admins).
	if firstNotif == nil {
		return nil
	}

	// Email: resolve recipients (explicit config list, fall back to admin emails).
	emailTo := s.configuredRecipients(ctx)
	if len(emailTo) == 0 {
		emailTo = emailRecipients
	}
	capturedEmailTo := emailTo // capture for closure
	s.enqueueAndSend(ctx, firstNotif, "email", func() error {
		return s.email.Send(ctx, capturedEmailTo, ev.title, ev.body)
	})

	// Webhook: one delivery per event.
	s.enqueueAndSend(ctx, firstNotif, "webhook", func() error {
		return s.webhook.Send(ctx, ev.notifType, ev.title, ev.body, ev.payload)
	})

	return nil
}

// enqueueAndSend creates a NotificationDelivery row with status="pending" and
// immediately attempts an optimistic send. If the send succeeds the row is
// marked "sent". If it fails the row stays "pending" with next_retry_at=nil
// so the retrier picks it up on the next tick.
//
// The attempts counter is NOT incremented here — it counts only retrier
// attempts, not the initial optimistic attempt.
func (s *NotificationService) enqueueAndSend(ctx context.Context, n *db.Notification, deliveryType string, send func() error) {
	d := &db.NotificationDelivery{
		NotificationID: n.ID,
		Type:           deliveryType,
		Status:         "pending",
	}
	if err := s.notifRepo.CreateDelivery(ctx, d); err != nil {
		s.logger.Error("failed to create delivery row",
			zap.String("notification_id", n.ID.String()),
			zap.String("type", deliveryType),
			zap.Error(err),
		)
		return
	}

	if err := send(); err != nil {
		s.logger.Warn("optimistic delivery failed — will retry",
			zap.String("notification_id", n.ID.String()),
			zap.String("type", deliveryType),
			zap.Error(err),
		)
		// Row stays pending with next_retry_at=nil → retrier will pick it up.
		d.LastError = truncateError(err)
		if updateErr := s.notifRepo.UpdateDelivery(ctx, d); updateErr != nil {
			s.logger.Error("failed to update delivery row after send failure",
				zap.String("delivery_id", d.ID.String()),
				zap.Error(updateErr),
			)
		}
		return
	}

	d.Status = "sent"
	if err := s.notifRepo.UpdateDelivery(ctx, d); err != nil {
		s.logger.Error("failed to mark delivery as sent",
			zap.String("delivery_id", d.ID.String()),
			zap.Error(err),
		)
	}
}

// -----------------------------------------------------------------------------
// Background retrier
// -----------------------------------------------------------------------------

const maxRetryAttempts = 3

// retryBackoff returns the next_retry_at time for a given attempt count.
// attempt is the number of retries already performed (post-initial-send).
//
//	attempt=0 (first retry after initial failure) → +5 min
//	attempt=1 (second retry)                      → +30 min
func retryBackoff(attempt int) time.Time {
	switch attempt {
	case 0:
		return time.Now().Add(5 * time.Minute)
	default:
		return time.Now().Add(30 * time.Minute)
	}
}

// processDeliveries is called by the retrier ticker. It fetches pending
// delivery rows and attempts to resend them, applying backoff on failure
// and marking exhausted after maxRetryAttempts failures.
func (s *NotificationService) processDeliveries(ctx context.Context) {
	rows, err := s.notifRepo.ListPendingDeliveries(ctx, time.Now(), 100)
	if err != nil {
		s.logger.Error("retrier: failed to list pending deliveries", zap.Error(err))
		return
	}

	for _, d := range rows {
		s.retryDelivery(ctx, d)
	}
}

// retryDelivery attempts to resend a single delivery row.
func (s *NotificationService) retryDelivery(ctx context.Context, d *db.NotificationDelivery) {
	// Load the parent notification to reconstruct content.
	n, err := s.notifRepo.GetByID(ctx, d.NotificationID)
	if err != nil {
		// Notification was deleted — the CASCADE should have removed the delivery
		// row too, but handle gracefully just in case.
		s.logger.Warn("retrier: parent notification not found, skipping",
			zap.String("delivery_id", d.ID.String()),
			zap.String("notification_id", d.NotificationID.String()),
		)
		return
	}

	var sendErr error
	switch d.Type {
	case "email":
		to := s.configuredRecipients(ctx)
		sendErr = s.email.Send(ctx, to, n.Title, n.Body)
	case "webhook":
		var payload map[string]any
		if jsonErr := json.Unmarshal([]byte(n.Payload), &payload); jsonErr != nil {
			payload = map[string]any{}
		}
		sendErr = s.webhook.Send(ctx, n.Type, n.Title, n.Body, payload)
	default:
		s.logger.Warn("retrier: unknown delivery type, skipping",
			zap.String("delivery_id", d.ID.String()),
			zap.String("type", d.Type),
		)
		return
	}

	d.Attempts++

	if sendErr == nil {
		d.Status = "sent"
		d.LastError = ""
		s.logger.Info("retrier: delivery succeeded",
			zap.String("delivery_id", d.ID.String()),
			zap.String("type", d.Type),
			zap.Int("attempt", d.Attempts),
		)
	} else {
		d.LastError = truncateError(sendErr)
		s.logger.Warn("retrier: delivery failed",
			zap.String("delivery_id", d.ID.String()),
			zap.String("type", d.Type),
			zap.Int("attempt", d.Attempts),
			zap.Error(sendErr),
		)
		if d.Attempts >= maxRetryAttempts {
			d.Status = "exhausted"
		} else {
			next := retryBackoff(d.Attempts)
			d.NextRetryAt = &next
		}
	}

	if err := s.notifRepo.UpdateDelivery(ctx, d); err != nil {
		s.logger.Error("retrier: failed to update delivery row",
			zap.String("delivery_id", d.ID.String()),
			zap.Error(err),
		)
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// configuredRecipients loads the explicit email recipient list from settings.
// Returns nil (not an empty slice) when not configured so the caller can
// distinguish "not set" from "empty list" and fall back to admin emails.
func (s *NotificationService) configuredRecipients(ctx context.Context) []string {
	settings, err := s.settingsRepo.GetMany(ctx, "notification.")
	if err != nil || len(settings) == 0 {
		return nil
	}
	raw := settingsIndex(settings)[KeyNotificationRecipients]
	if raw == "" {
		return nil
	}
	var out []string
	for _, r := range strings.Split(raw, ",") {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

// truncateError returns the error message truncated to 500 characters to avoid
// storing unbounded data in the last_error column.
func truncateError(err error) string {
	msg := err.Error()
	if len(msg) > 500 {
		return msg[:500]
	}
	return msg
}
