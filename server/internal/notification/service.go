package notification

import (
	"context"
	"encoding/json"
	"fmt"
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

// notificationService is the concrete implementation of Service.
type notificationService struct {
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

// NewService creates a new notification Service. The email and webhook senders
// are wired internally — callers only need to provide the Config dependencies.
func NewService(cfg Config) Service {
	svc := &notificationService{
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

// -----------------------------------------------------------------------------
// Public typed methods
// -----------------------------------------------------------------------------

func (s *notificationService) NotifyJobSucceeded(ctx context.Context, jobID, policyID uuid.UUID, policyName string) error {
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

func (s *notificationService) NotifyJobFailed(ctx context.Context, jobID, policyID uuid.UUID, policyName, errMsg string) error {
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

func (s *notificationService) NotifyAgentOffline(ctx context.Context, agentID uuid.UUID, agentName string) error {
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
//  2. Persists one db.Notification per recipient.
//  3. Publishes each notification to the WebSocket Hub.
//  4. Fans out to email and webhook (errors are logged, not returned, so that
//     an SMTP failure never prevents the in-app notification from being saved).
func (s *notificationService) notify(ctx context.Context, ev event) error {
	// Resolve all admin users — they are the recipients for all system events.
	// A large page size is used because the number of admins is expected to be
	// small (typically 1-3 in a self-hosted setup).
	admins, _, err := s.userRepo.List(ctx, repositories.ListOptions{Limit: 100, Offset: 0})
	if err != nil {
		return fmt.Errorf("notification: failed to list users: %w", err)
	}

	payloadJSON, err := json.Marshal(ev.payload)
	if err != nil {
		return fmt.Errorf("notification: failed to marshal payload: %w", err)
	}

	var emailRecipients []string

	for i := range admins {
		u := &admins[i]
		if u.Role != "admin" || !u.IsActive {
			continue
		}

		// Persist the in-app notification.
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

		// Publish to the WebSocket Hub so any connected GUI tab receives the
		// notification instantly without polling.
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

	// External channels: errors are logged but not propagated — the in-app
	// notification has already been saved, which is the authoritative channel.
	if err := s.email.Send(ctx, emailRecipients, ev.title, ev.body); err != nil {
		s.logger.Warn("email notification delivery failed",
			zap.String("type", ev.notifType),
			zap.Error(err),
		)
	}

	if err := s.webhook.Send(ctx, ev.notifType, ev.title, ev.body, ev.payload); err != nil {
		s.logger.Warn("webhook notification delivery failed",
			zap.String("type", ev.notifType),
			zap.Error(err),
		)
	}

	return nil
}