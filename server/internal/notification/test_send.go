package notification

import (
	"context"
	"fmt"
)

// SendTestEmail sends a test message with cfg to the single address to,
// through the same code path as real notifications. Used by the SMTP settings
// page to check a configuration before (or after) saving it.
func SendTestEmail(ctx context.Context, cfg SMTPConfig, to string) error {
	s := newEmailSender(func(context.Context) (*SMTPConfig, error) { return &cfg, nil })
	return s.Send(ctx, []string{to}, "Arkeep test email",
		fmt.Sprintf("This is a test email from Arkeep, sent through %s:%d.\r\n\r\nIf you can read it, notifications by email work.", cfg.Host, cfg.Port))
}

// TestWebhookType is the event type of a test webhook, so receivers can tell
// it apart from real events.
const TestWebhookType = "test"

// SendTestWebhook posts a test event with cfg, signed like real events when a
// secret is set. The webhook does not need to be enabled.
func SendTestWebhook(ctx context.Context, cfg WebhookConfig) error {
	cfg.Enabled = true
	s := newWebhookSender(func(context.Context) (*WebhookConfig, error) { return &cfg, nil })
	return s.Send(ctx, TestWebhookType, "Arkeep test webhook",
		"This is a test webhook from Arkeep. If you received it, webhook notifications work.", nil)
}
