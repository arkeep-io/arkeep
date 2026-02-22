package notification

import "errors"

// Sentinel errors returned by the notification service and its senders.
// Callers should use errors.Is for comparison.
var (
	// ErrSendFailed is returned when a notification could not be delivered
	// through one or more channels (email, webhook). It wraps the underlying
	// cause and is non-fatal — the in-app notification is still persisted even
	// if external delivery fails.
	ErrSendFailed = errors.New("notification: send failed")

	// ErrConfigNotFound is returned when a required configuration key is
	// missing from the settings table (e.g. SMTP not configured yet).
	ErrConfigNotFound = errors.New("notification: configuration not found")

	// ErrInvalidConfig is returned when settings exist but contain invalid or
	// incomplete values (e.g. SMTP host present but port missing).
	ErrInvalidConfig = errors.New("notification: invalid configuration")
)