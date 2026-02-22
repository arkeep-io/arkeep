package notification

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// webhookPayload is the JSON body sent to the webhook endpoint.
// The structure is kept generic and compatible with Slack/Discord/Teams
// incoming webhook formats via the "text" field, while also carrying
// structured data in "payload" for custom integrations.
type webhookPayload struct {
	Type      string         `json:"type"`
	Title     string         `json:"title"`
	Body      string         `json:"text"` // "text" for Slack/Discord compatibility
	Payload   map[string]any `json:"payload,omitempty"`
	Timestamp string         `json:"timestamp"`
}

// webhookSender delivers notifications via an outbound HTTP POST to a
// configured URL. Optionally signs the request body with HMAC-SHA256 when
// a secret is configured, enabling the receiver to verify authenticity.
type webhookSender struct {
	client *http.Client
	loader func(ctx context.Context) (*WebhookConfig, error)
}

// newWebhookSender creates a webhookSender. loader is called on every Send
// to retrieve the current webhook configuration from the settings repository.
func newWebhookSender(loader func(ctx context.Context) (*WebhookConfig, error)) *webhookSender {
	return &webhookSender{
		client: &http.Client{Timeout: 10 * time.Second},
		loader: loader,
	}
}

// Send serializes the notification as JSON and POSTs it to the configured
// webhook URL. If the webhook is disabled or not configured, the send is
// skipped silently. Non-2xx responses are treated as delivery failures and
// returned wrapped in ErrSendFailed.
func (s *webhookSender) Send(ctx context.Context, notifType, title, body string, payload map[string]any) error {
	cfg, err := s.loader(ctx)
	if err != nil {
		if err == ErrConfigNotFound {
			// Webhook not configured — skip silently.
			return nil
		}
		return fmt.Errorf("%w: failed to load webhook config: %s", ErrSendFailed, err)
	}

	if !cfg.Enabled {
		return nil
	}

	data, err := json.Marshal(webhookPayload{
		Type:      notifType,
		Title:     title,
		Body:      body,
		Payload:   payload,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("%w: failed to marshal webhook payload: %s", ErrSendFailed, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%w: failed to build webhook request: %s", ErrSendFailed, err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Arkeep-Webhook/1.0")

	// Sign the request body with HMAC-SHA256 if a secret is configured.
	// The signature is sent in the X-Arkeep-Signature header as "sha256=<hex>",
	// following the convention used by GitHub and Stripe webhooks.
	if cfg.Secret != "" {
		sig := hmacSHA256(data, cfg.Secret)
		req.Header.Set("X-Arkeep-Signature", "sha256="+sig)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: webhook request failed: %s", ErrSendFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: webhook returned non-2xx status %d", ErrSendFailed, resp.StatusCode)
	}

	return nil
}

// hmacSHA256 computes an HMAC-SHA256 signature of data using secret,
// returned as a lowercase hex string.
func hmacSHA256(data []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}