// Package notification implements the notification service for Arkeep.
// It is the single component responsible for persisting in-app notifications,
// publishing them to the WebSocket Hub, and delivering them via external
// channels (email, webhook). No other package should write to the notifications
// table or call hub.Publish on notification topics directly.
package notification

import (
	"context"
	"fmt"
	"strconv"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// Setting keys used by the notification service.
// All keys are namespaced to avoid collisions with future config namespaces.
const (
	KeySMTPHost     = "smtp.host"
	KeySMTPPort     = "smtp.port"
	KeySMTPUsername = "smtp.username"
	KeySMTPPassword = "smtp.password" // stored encrypted via EncryptedString
	KeySMTPFrom     = "smtp.from"
	KeySMTPFromName = "smtp.from_name" // optional display name for the From header
	KeySMTPTLS      = "smtp.tls"       // "true" or "false"

	KeyWebhookURL     = "webhook.url"
	KeyWebhookSecret  = "webhook.secret"  // HMAC secret, stored encrypted
	KeyWebhookEnabled = "webhook.enabled" // "true" or "false"

	// KeyNotificationRecipients is a comma-separated list of email addresses
	// that receive email notifications. When empty the service falls back to
	// all active admin users' email addresses.
	KeyNotificationRecipients = "notification.recipients"

	// Per-event toggle keys — "true" or "false", default true except agent_online.
	KeyEventJobSuccess   = "notification.events.job_success"
	KeyEventJobFailure   = "notification.events.job_failure"
	KeyEventAgentOffline = "notification.events.agent_offline"
	KeyEventAgentOnline  = "notification.events.agent_online"
)

// DefaultFromName is used as the email sender display name when smtp.from_name
// is not configured, so notifications read as "Arkeep <address>" out of the box.
const DefaultFromName = "Arkeep"

// SMTPConfig holds the configuration needed to send emails via SMTP.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string // decrypted at load time by EncryptedString.Scan
	From     string
	FromName string // display name for the From header; defaults to DefaultFromName
	TLS      bool   // true = STARTTLS / implicit TLS
}

// WebhookConfig holds the configuration for the outbound HTTP webhook channel.
type WebhookConfig struct {
	URL     string
	Secret  string // optional HMAC-SHA256 signing secret, decrypted at load time
	Enabled bool
}

// loadSMTPConfig reads all "smtp.*" settings from the repository and assembles
// an SMTPConfig. Returns ErrConfigNotFound if no SMTP settings exist at all,
// ErrInvalidConfig if required fields are missing or malformed.
func loadSMTPConfig(ctx context.Context, repo repositories.SettingsRepository) (*SMTPConfig, error) {
	settings, err := repo.GetMany(ctx, "smtp.")
	if err != nil {
		return nil, fmt.Errorf("notification: failed to load smtp settings: %w", err)
	}
	if len(settings) == 0 {
		return nil, ErrConfigNotFound
	}

	// Index by key for convenient lookup.
	idx := settingsIndex(settings)

	host := idx[KeySMTPHost]
	if host == "" {
		return nil, fmt.Errorf("%w: smtp.host is required", ErrInvalidConfig)
	}

	portStr := idx[KeySMTPPort]
	if portStr == "" {
		return nil, fmt.Errorf("%w: smtp.port is required", ErrInvalidConfig)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("%w: smtp.port must be a valid port number", ErrInvalidConfig)
	}

	from := idx[KeySMTPFrom]
	if from == "" {
		return nil, fmt.Errorf("%w: smtp.from is required", ErrInvalidConfig)
	}

	// Optional display name; fall back to the default so the sender always has
	// a friendly name even on installs that never configured one.
	fromName := idx[KeySMTPFromName]
	if fromName == "" {
		fromName = DefaultFromName
	}

	tls := idx[KeySMTPTLS] == "true"

	return &SMTPConfig{
		Host:     host,
		Port:     port,
		Username: idx[KeySMTPUsername],
		Password: idx[KeySMTPPassword],
		From:     from,
		FromName: fromName,
		TLS:      tls,
	}, nil
}

// loadWebhookConfig reads all "webhook.*" settings from the repository.
// Returns ErrConfigNotFound if no webhook settings exist.
func loadWebhookConfig(ctx context.Context, repo repositories.SettingsRepository) (*WebhookConfig, error) {
	settings, err := repo.GetMany(ctx, "webhook.")
	if err != nil {
		return nil, fmt.Errorf("notification: failed to load webhook settings: %w", err)
	}
	if len(settings) == 0 {
		return nil, ErrConfigNotFound
	}

	idx := settingsIndex(settings)

	url := idx[KeyWebhookURL]
	if url == "" {
		return nil, fmt.Errorf("%w: webhook.url is required", ErrInvalidConfig)
	}

	enabled := idx[KeyWebhookEnabled] == "true"

	return &WebhookConfig{
		URL:     url,
		Secret:  idx[KeyWebhookSecret],
		Enabled: enabled,
	}, nil
}

// NotificationEventsConfig holds the per-event enable/disable toggles for
// external deliveries (email + webhook). In-app notifications are unaffected.
// Default: all true except AgentOnline.
type NotificationEventsConfig struct {
	JobSuccess   bool
	JobFailure   bool
	AgentOffline bool
	AgentOnline  bool
}

// loadNotificationEventsConfig reads the four event toggle keys from the
// settings repository and returns the config with the following defaults:
//
//	job_success=true, job_failure=true, agent_offline=true, agent_online=false
func loadNotificationEventsConfig(ctx context.Context, repo repositories.SettingsRepository) NotificationEventsConfig {
	cfg := NotificationEventsConfig{
		JobSuccess:   true,
		JobFailure:   true,
		AgentOffline: true,
		AgentOnline:  false,
	}
	settings, err := repo.GetMany(ctx, "notification.events.")
	if err != nil || len(settings) == 0 {
		return cfg
	}
	idx := settingsIndex(settings)
	if v, ok := idx[KeyEventJobSuccess]; ok {
		cfg.JobSuccess = v == "true"
	}
	if v, ok := idx[KeyEventJobFailure]; ok {
		cfg.JobFailure = v == "true"
	}
	if v, ok := idx[KeyEventAgentOffline]; ok {
		cfg.AgentOffline = v == "true"
	}
	if v, ok := idx[KeyEventAgentOnline]; ok {
		cfg.AgentOnline = v == "true"
	}
	return cfg
}

// settingsIndex converts a slice of Setting into a map[key]value string for
// convenient O(1) lookup. EncryptedString.String() returns the decrypted
// plaintext — decryption has already occurred when GORM scanned the row.
func settingsIndex(settings []db.Setting) map[string]string {
	idx := make(map[string]string, len(settings))
	for _, s := range settings {
		idx[s.Key] = string(s.Value)
	}
	return idx
}