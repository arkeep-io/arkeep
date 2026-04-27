package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// createDBNotification inserts a notification for the given userID directly.
func createDBNotification(t *testing.T, deps *testDeps, userID uuid.UUID) *db.Notification {
	t.Helper()
	n := &db.Notification{
		UserID:  userID,
		Type:    "job_success",
		Title:   "Backup completed",
		Body:    "Policy ran successfully.",
		Payload: "{}",
	}
	if err := deps.notifs.Create(context.Background(), n); err != nil {
		t.Fatalf("createDBNotification: %v", err)
	}
	return n
}

func TestNotificationHandler_List(t *testing.T) {
	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/notifications", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("returns empty list for user with no notifications", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/notifications", e.userToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		}
		decodeData(t, resp, &data)
		if data.Total != 0 {
			t.Errorf("total = %d, want 0", data.Total)
		}
	})

	t.Run("returns notifications scoped to authenticated user", func(t *testing.T) {
		e := newTestEnv(t)
		userID := createDBUser(t, e.deps, "notif-user@example.com", "user")
		token := e.tokenForUser(t, userID, "user")

		createDBNotification(t, e.deps, userID)
		createDBNotification(t, e.deps, userID)
		// Notification for a different user — should not appear.
		createDBNotification(t, e.deps, uuid.New())

		resp := e.get(t, "/api/v1/notifications", token)
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		}
		decodeData(t, resp, &data)
		if data.Total != 2 {
			t.Errorf("total = %d, want 2 (scoped to user)", data.Total)
		}
	})
}

func TestNotificationHandler_MarkAsRead(t *testing.T) {
	t.Run("marks notification as read", func(t *testing.T) {
		e := newTestEnv(t)
		userID := createDBUser(t, e.deps, "reader@example.com", "user")
		token := e.tokenForUser(t, userID, "user")

		n := createDBNotification(t, e.deps, userID)

		resp := e.patch(t, "/api/v1/notifications/"+n.ID.String()+"/read", token, nil)
		assertStatus(t, resp, http.StatusNoContent)
	})

	t.Run("returns 404 for non-existent notification", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.patch(t, "/api/v1/notifications/00000000-0000-0000-0000-000000000001/read",
			e.userToken(t), nil)
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 404 when notification belongs to another user", func(t *testing.T) {
		e := newTestEnv(t)
		// Create notification owned by a different user.
		n := createDBNotification(t, e.deps, uuid.New())

		resp := e.patch(t, "/api/v1/notifications/"+n.ID.String()+"/read",
			e.userToken(t), nil)
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 400 for malformed UUID", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.patch(t, "/api/v1/notifications/not-a-uuid/read", e.userToken(t), nil)
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.patch(t, "/api/v1/notifications/00000000-0000-0000-0000-000000000001/read", "", nil)
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestNotificationHandler_MarkAllAsRead(t *testing.T) {
	t.Run("marks all notifications as read", func(t *testing.T) {
		e := newTestEnv(t)
		userID := createDBUser(t, e.deps, "markall@example.com", "user")
		token := e.tokenForUser(t, userID, "user")

		createDBNotification(t, e.deps, userID)
		createDBNotification(t, e.deps, userID)

		resp := e.patch(t, "/api/v1/notifications/read-all", token, nil)
		assertStatus(t, resp, http.StatusNoContent)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.patch(t, "/api/v1/notifications/read-all", "", nil)
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestNotificationHandler_ListDeliveryQueue(t *testing.T) {
	t.Run("admin can list delivery queue", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/notifications/queue", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		}
		decodeData(t, resp, &data)
		if data.Total != 0 {
			t.Errorf("total = %d, want 0", data.Total)
		}
	})

	t.Run("returns 400 for invalid status filter", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/notifications/queue?status=unknown", e.adminToken(t))
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("accepts valid status filters", func(t *testing.T) {
		for _, status := range []string{"pending", "sent", "exhausted"} {
			e := newTestEnv(t)
			resp := e.get(t, "/api/v1/notifications/queue?status="+status, e.adminToken(t))
			assertStatus(t, resp, http.StatusOK)
		}
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/notifications/queue", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}
