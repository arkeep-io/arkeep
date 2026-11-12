package notification

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// createAdmin adds an active admin, so notify has someone to notify.
func createAdmin(t *testing.T, gdb *gorm.DB) *db.User {
	t.Helper()
	admin := &db.User{Email: "admin@test.local", DisplayName: "Admin", Role: "admin", IsActive: true}
	if err := repositories.NewUserRepository(gdb).Create(context.Background(), admin); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	return admin
}

// emailDeliveries returns the email delivery rows of every status.
func emailDeliveries(t *testing.T, gdb *gorm.DB) []db.NotificationDelivery {
	t.Helper()
	var out []db.NotificationDelivery
	if err := gdb.Where("type = ?", "email").Find(&out).Error; err != nil {
		t.Fatalf("list email deliveries: %v", err)
	}
	return out
}

// TestNotify_EmailDeliveryNeedsSMTP checks that an event creates an email
// delivery only when SMTP is usable: a missing or incomplete configuration
// sends nothing, so no delivery may be recorded, least of all as sent (#430).
func TestNotify_EmailDeliveryNeedsSMTP(t *testing.T) {
	tests := []struct {
		name      string
		smtp      func(t *testing.T) map[string]string
		wantEmail bool
	}{
		{"not configured", func(*testing.T) map[string]string { return nil }, false},
		{"incomplete", func(*testing.T) map[string]string {
			return map[string]string{KeySMTPHost: "127.0.0.1"}
		}, false},
		{"configured", func(t *testing.T) map[string]string {
			port, _ := startFakeSMTP(t)
			return map[string]string{KeySMTPHost: "127.0.0.1", KeySMTPPort: strconv.Itoa(port), KeySMTPFrom: "noreply@test.local"}
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			svc, gdb := newTestService(t)
			setSettings(t, repositories.NewSettingsRepository(gdb), tt.smtp(t))
			createAdmin(t, gdb)

			ev := event{notifType: "agent_offline", title: "Agent offline", body: "web-01 is offline", payload: map[string]any{"agent_name": "web-01"}}
			if err := svc.notify(ctx, ev); err != nil {
				t.Fatalf("notify: %v", err)
			}

			got := emailDeliveries(t, gdb)
			if !tt.wantEmail {
				if len(got) != 0 {
					t.Errorf("got email deliveries %+v, want none", got)
				}
				return
			}
			if len(got) != 1 || got[0].Status != "sent" {
				t.Errorf("got email deliveries %+v, want one sent", got)
			}
		})
	}
}

// TestRetryDelivery_EmailUndeliverable checks that a pending email delivery
// is given up, not marked sent, when it can no longer be sent at all: SMTP
// was removed or left incomplete after the delivery was created, or nobody is
// left to send it to. No attempt is counted, since none was made.
func TestRetryDelivery_EmailUndeliverable(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, gdb *gorm.DB, admin *db.User)
		wantError string
	}{
		{"SMTP removed", func(*testing.T, *gorm.DB, *db.User) {}, "SMTP is not configured"},
		{"SMTP incomplete", func(t *testing.T, gdb *gorm.DB, _ *db.User) {
			setSettings(t, repositories.NewSettingsRepository(gdb), map[string]string{KeySMTPHost: "127.0.0.1"})
		}, "SMTP is not configured"},
		{"no recipients", func(t *testing.T, gdb *gorm.DB, admin *db.User) {
			port, _ := startFakeSMTP(t)
			setSettings(t, repositories.NewSettingsRepository(gdb), map[string]string{KeySMTPHost: "127.0.0.1", KeySMTPPort: strconv.Itoa(port), KeySMTPFrom: "noreply@test.local"})
			// The only admin was deactivated and no recipient list is set.
			admin.IsActive = false
			if err := repositories.NewUserRepository(gdb).Update(context.Background(), admin); err != nil {
				t.Fatalf("deactivate admin: %v", err)
			}
		}, "no email recipients"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			svc, gdb := newTestService(t)
			notifs := repositories.NewNotificationRepository(gdb)
			admin := createAdmin(t, gdb)

			n := &db.Notification{UserID: admin.ID, Type: "agent_offline", Title: "t", Body: "b", Payload: "{}"}
			if err := notifs.Create(ctx, n); err != nil {
				t.Fatalf("create notification: %v", err)
			}
			d := &db.NotificationDelivery{NotificationID: n.ID, Type: "email", Status: "pending"}
			if err := notifs.CreateDelivery(ctx, d); err != nil {
				t.Fatalf("create delivery: %v", err)
			}
			tt.setup(t, gdb, admin)

			svc.retryDelivery(ctx, d)

			got := emailDeliveries(t, gdb)
			if len(got) != 1 {
				t.Fatalf("got email deliveries %+v, want one", got)
			}
			if got[0].Status != "exhausted" || got[0].Attempts != 0 || !strings.Contains(got[0].LastError, tt.wantError) {
				t.Errorf("delivery status %q, attempts %d, last error %q; want exhausted, 0 attempts, error containing %q",
					got[0].Status, got[0].Attempts, got[0].LastError, tt.wantError)
			}
		})
	}
}
