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
	KeySMTPTLS      = "smtp.tls" // "true" or "false"

	KeyWebhookURL     = "webhook.url"
	KeyWebhookSecret  = "webhook.secret"  // HMAC secret, stored encrypted
	KeyWebhookEnabled = "webhook.enabled" // "true" or "false"
)

// SMTPConfig holds the configuration needed to send emails via SMTP.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string // decrypted at load time by EncryptedString.Scan
	From     string
	TLS      bool // true = STARTTLS / implicit TLS
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

	tls := idx[KeySMTPTLS] == "true"

	return &SMTPConfig{
		Host:     host,
		Port:     port,
		Username: idx[KeySMTPUsername],
		Password: idx[KeySMTPPassword],
		From:     from,
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