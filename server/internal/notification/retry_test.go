package notification

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/db/dbtest"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
)

// newRetryTestService returns a service backed by an in-memory database.
func newRetryTestService(t *testing.T) (*NotificationService, repositories.UserRepository, repositories.SettingsRepository, repositories.NotificationRepository) {
	t.Helper()
	if err := db.InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("db.InitEncryption: %v", err)
	}
	gdb := dbtest.New(t)
	users := repositories.NewUserRepository(gdb)
	settings := repositories.NewSettingsRepository(gdb)
	notifs := repositories.NewNotificationRepository(gdb)
	svc := NewService(Config{
		NotifRepo:    notifs,
		UserRepo:     users,
		SettingsRepo: settings,
		PolicyRepo:   repositories.NewPolicyRepository(gdb),
		Hub:          websocket.NewHub(),
		Logger:       zap.NewNop(),
	})
	return svc, users, settings, notifs
}

func TestRetryBackoff(t *testing.T) {
	tests := []struct {
		retries int
		want    time.Duration
	}{
		{1, 5 * time.Minute},
		{2, 30 * time.Minute},
	}
	for _, tt := range tests {
		got := time.Until(retryBackoff(tt.retries))
		if got < tt.want-time.Minute || got > tt.want {
			t.Errorf("retryBackoff(%d) = +%v, want +%v", tt.retries, got.Round(time.Second), tt.want)
		}
	}
}

// TestRetryDelivery_Schedule follows a webhook that keeps failing through the
// retrier: the documented schedule is a retry on the next tick, then after 5
// minutes, then after 30 minutes, then the delivery is given up.
func TestRetryDelivery_Schedule(t *testing.T) {
	ctx := context.Background()
	svc, users, settings, notifs := newRetryTestService(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	for k, v := range map[string]string{KeyWebhookURL: srv.URL, KeyWebhookEnabled: "true"} {
		if err := settings.Set(ctx, k, db.EncryptedString(v)); err != nil {
			t.Fatalf("settings.Set(%s): %v", k, err)
		}
	}

	admin := &db.User{Email: "admin@test.local", DisplayName: "Admin", Role: "admin", IsActive: true}
	if err := users.Create(ctx, admin); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	n := &db.Notification{UserID: admin.ID, Type: "job_failure", Title: "t", Body: "b", Payload: "{}"}
	if err := notifs.Create(ctx, n); err != nil {
		t.Fatalf("create notification: %v", err)
	}
	// The optimistic send failed: the row is pending with no next_retry_at.
	d := &db.NotificationDelivery{NotificationID: n.ID, Type: "webhook", Status: "pending"}
	if err := notifs.CreateDelivery(ctx, d); err != nil {
		t.Fatalf("create delivery: %v", err)
	}

	for i, want := range []time.Duration{5 * time.Minute, 30 * time.Minute} {
		svc.retryDelivery(ctx, d)
		if d.Status != "pending" || d.NextRetryAt == nil {
			t.Fatalf("retry %d: status %q, next_retry_at %v; want pending with a next retry", i+1, d.Status, d.NextRetryAt)
		}
		if got := time.Until(*d.NextRetryAt); got < want-time.Minute || got > want {
			t.Errorf("retry %d: next retry in %v, want %v", i+1, got.Round(time.Second), want)
		}
	}
	svc.retryDelivery(ctx, d)
	if d.Status != "exhausted" || d.Attempts != maxRetryAttempts {
		t.Errorf("after the last retry: status %q, attempts %d; want exhausted after %d", d.Status, d.Attempts, maxRetryAttempts)
	}
}

// TestEmailRecipients checks that a retried email reaches the same people as
// the first send: the configured list, or every active admin without one.
func TestEmailRecipients(t *testing.T) {
	ctx := context.Background()
	svc, users, settings, _ := newRetryTestService(t)

	for _, u := range []*db.User{
		{Email: "a1@test.local", DisplayName: "A1", Role: "admin", IsActive: true},
		{Email: "a2@test.local", DisplayName: "A2", Role: "admin", IsActive: true},
		{Email: "off@test.local", DisplayName: "Off", Role: "admin", IsActive: false},
		{Email: "user@test.local", DisplayName: "User", Role: "user", IsActive: true},
	} {
		active := u.IsActive
		if err := users.Create(ctx, u); err != nil {
			t.Fatalf("create %s: %v", u.Email, err)
		}
		// is_active has a database default, so false is only stored by Update.
		if !active {
			u.IsActive = false
			if err := users.Update(ctx, u); err != nil {
				t.Fatalf("deactivate %s: %v", u.Email, err)
			}
		}
	}

	got, err := svc.emailRecipients(ctx)
	if err != nil {
		t.Fatalf("emailRecipients: %v", err)
	}
	slices.Sort(got)
	if want := []string{"a1@test.local", "a2@test.local"}; !slices.Equal(got, want) {
		t.Errorf("without a recipient list = %v, want the active admins %v", got, want)
	}

	if err := settings.Set(ctx, KeyNotificationRecipients, db.EncryptedString("ops@test.local, oncall@test.local")); err != nil {
		t.Fatalf("set recipients: %v", err)
	}
	got, err = svc.emailRecipients(ctx)
	if err != nil {
		t.Fatalf("emailRecipients: %v", err)
	}
	if want := []string{"ops@test.local", "oncall@test.local"}; !slices.Equal(got, want) {
		t.Errorf("with a recipient list = %v, want %v", got, want)
	}
}
