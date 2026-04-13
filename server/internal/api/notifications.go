package api

import (
	"errors"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// NotificationHandler groups all notification-related HTTP handlers.
// Notifications are scoped to the authenticated user — each user can only
// see and manage their own notifications.
type NotificationHandler struct {
	repo   repositories.NotificationRepository
	logger *zap.Logger
}

// NewNotificationHandler creates a new NotificationHandler.
func NewNotificationHandler(repo repositories.NotificationRepository, logger *zap.Logger) *NotificationHandler {
	return &NotificationHandler{
		repo:   repo,
		logger: logger.Named("notification_handler"),
	}
}

// -----------------------------------------------------------------------------
// Response types
// -----------------------------------------------------------------------------

// notificationResponse is the JSON representation of a notification.
type notificationResponse struct {
	ID        string  `json:"id"`
	Type      string  `json:"type"`
	Title     string  `json:"title"`
	Body      string  `json:"body"`
	Payload   string  `json:"payload"`
	ReadAt    *string `json:"read_at"`
	CreatedAt string  `json:"created_at"`
}

// notificationToResponse converts a db.Notification to a notificationResponse.
func notificationToResponse(n *db.Notification) notificationResponse {
	resp := notificationResponse{
		ID:        n.ID.String(),
		Type:      n.Type,
		Title:     n.Title,
		Body:      n.Body,
		Payload:   n.Payload,
		CreatedAt: n.CreatedAt.UTC().Format(time.RFC3339),
	}
	if n.ReadAt != nil {
		s := n.ReadAt.UTC().Format(time.RFC3339)
		resp.ReadAt = &s
	}
	return resp
}

// listNotificationsResponse wraps a paginated list of notifications.
type listNotificationsResponse struct {
	Items []notificationResponse `json:"items"`
	Total int64                  `json:"total"`
}

// -----------------------------------------------------------------------------
// Handlers
// -----------------------------------------------------------------------------

// List handles GET /api/v1/notifications.
// Returns a paginated list of notifications for the authenticated user,
// ordered by creation time descending (most recent first).
func (h *NotificationHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	if claims == nil {
		ErrUnauthorized(w)
		return
	}

	userID, err := parseUUIDString(claims.UserID)
	if err != nil {
		ErrInternal(w)
		return
	}

	opts := paginationOpts(r)

	notifications, total, err := h.repo.ListByUser(r.Context(), userID, opts)
	if err != nil {
		h.logger.Error("failed to list notifications",
			zap.String("user_id", claims.UserID),
			zap.Error(err),
		)
		ErrInternal(w)
		return
	}

	items := make([]notificationResponse, len(notifications))
	for i := range notifications {
		items[i] = notificationToResponse(&notifications[i])
	}

	Ok(w, listNotificationsResponse{Items: items, Total: total})
}

// MarkAsRead handles PATCH /api/v1/notifications/{id}/read.
// Marks a single notification as read. Returns 404 if the notification does
// not exist or is already marked as read (idempotent from the client's
// perspective — the desired state is already met).
func (h *NotificationHandler) MarkAsRead(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}

	// Verify the notification belongs to the authenticated user before
	// marking it as read to prevent cross-user access.
	claims := claimsFromCtx(r.Context())
	if claims == nil {
		ErrUnauthorized(w)
		return
	}

	notification, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			ErrNotFound(w)
			return
		}
		h.logger.Error("failed to get notification", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	if notification.UserID.String() != claims.UserID {
		// Return 404 instead of 403 to avoid leaking that the notification
		// exists but belongs to another user.
		ErrNotFound(w)
		return
	}

	if err := h.repo.MarkAsRead(r.Context(), id); err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			// Already read — treat as success.
			NoContent(w)
			return
		}
		h.logger.Error("failed to mark notification as read", zap.String("id", id.String()), zap.Error(err))
		ErrInternal(w)
		return
	}

	NoContent(w)
}

// deliveryQueueResponse is the JSON representation of a delivery queue row.
type deliveryQueueResponse struct {
	ID             string  `json:"id"`
	NotificationID string  `json:"notification_id"`
	Type           string  `json:"type"`
	Status         string  `json:"status"`
	Attempts       int     `json:"attempts"`
	LastError      string  `json:"last_error"`
	NextRetryAt    *string `json:"next_retry_at"`
	CreatedAt      string  `json:"created_at"`
}

// listDeliveryQueueResponse wraps a paginated list of delivery rows.
type listDeliveryQueueResponse struct {
	Items []deliveryQueueResponse `json:"items"`
	Total int64                   `json:"total"`
}

// ListDeliveryQueue handles GET /api/v1/notifications/queue (admin only).
// Returns delivery rows filtered by ?status= (default: "exhausted").
// Used to inspect failed notification deliveries.
func (h *NotificationHandler) ListDeliveryQueue(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "exhausted"
	}
	switch status {
	case "pending", "sent", "exhausted":
	default:
		ErrBadRequest(w, "status must be one of: pending, sent, exhausted")
		return
	}

	opts := paginationOpts(r)
	rows, total, err := h.repo.ListDeliveriesByStatus(r.Context(), status, opts)
	if err != nil {
		h.logger.Error("failed to list delivery queue", zap.String("status", status), zap.Error(err))
		ErrInternal(w)
		return
	}

	items := make([]deliveryQueueResponse, len(rows))
	for i, d := range rows {
		item := deliveryQueueResponse{
			ID:             d.ID.String(),
			NotificationID: d.NotificationID.String(),
			Type:           d.Type,
			Status:         d.Status,
			Attempts:       d.Attempts,
			LastError:      d.LastError,
			CreatedAt:      d.CreatedAt.UTC().Format(time.RFC3339),
		}
		if d.NextRetryAt != nil {
			s := d.NextRetryAt.UTC().Format(time.RFC3339)
			item.NextRetryAt = &s
		}
		items[i] = item
	}

	Ok(w, listDeliveryQueueResponse{Items: items, Total: total})
}

// MarkAllAsRead handles PATCH /api/v1/notifications/read-all.
// Marks all unread notifications for the authenticated user as read.
func (h *NotificationHandler) MarkAllAsRead(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromCtx(r.Context())
	if claims == nil {
		ErrUnauthorized(w)
		return
	}

	userID, err := parseUUIDString(claims.UserID)
	if err != nil {
		ErrInternal(w)
		return
	}

	if err := h.repo.MarkAllAsRead(r.Context(), userID); err != nil {
		h.logger.Error("failed to mark all notifications as read",
			zap.String("user_id", claims.UserID),
			zap.Error(err),
		)
		ErrInternal(w)
		return
	}

	NoContent(w)
}