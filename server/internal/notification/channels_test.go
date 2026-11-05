package notification

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// setSettings stores each key/value pair.
func setSettings(t *testing.T, repo repositories.SettingsRepository, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		if err := repo.Set(context.Background(), k, db.EncryptedString(v)); err != nil {
			t.Fatalf("settings.Set(%s): %v", k, err)
		}
	}
}

// deliveryTypes returns the type of every delivery row, whatever its status.
func deliveryTypes(t *testing.T, notifs repositories.NotificationRepository) []string {
	t.Helper()
	var types []string
	for _, status := range []string{"pending", "sent", "exhausted"} {
		rows, _, err := notifs.ListDeliveriesByStatus(context.Background(), status, repositories.ListOptions{Limit: 100})
		if err != nil {
			t.Fatalf("ListDeliveriesByStatus(%s): %v", status, err)
		}
		for _, d := range rows {
			types = append(types, d.Type)
		}
	}
	return types
}

func TestChannelEnabled(t *testing.T) {
	tests := []struct {
		name     string
		settings map[string]string
		channel  string
		want     bool
	}{
		{"email is on by default", nil, "email", true},
		{"email turned off", map[string]string{KeyEmailEnabled: "false"}, "email", false},
		{"email turned on", map[string]string{KeyEmailEnabled: "true"}, "email", true},
		{"webhook is off by default", nil, "webhook", false},
		{"webhook turned on", map[string]string{KeyWebhookEnabled: "true"}, "webhook", true},
		{"webhook turned off", map[string]string{KeyWebhookEnabled: "false"}, "webhook", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, settings, _ := newRetryTestService(t)
			setSettings(t, settings, tt.settings)
			if got := channelEnabled(context.Background(), settings, tt.channel); got != tt.want {
				t.Errorf("channelEnabled(%q) = %v, want %v", tt.channel, got, tt.want)
			}
		})
	}
}

// TestNotify_Channels checks that an event gets a delivery row only on the
// channels that are turned on, and that the in-app notification is created
// either way.
func TestNotify_Channels(t *testing.T) {
	tests := []struct {
		name     string
		settings map[string]string
		want     []string
	}{
		{"defaults: email only", nil, []string{"email"}},
		{"email off, webhook off", map[string]string{KeyEmailEnabled: "false"}, nil},
		{"email off, webhook on", map[string]string{KeyEmailEnabled: "false", KeyWebhookEnabled: "true"}, []string{"webhook"}},
		{"both on", map[string]string{KeyWebhookEnabled: "true"}, []string{"email", "webhook"}},
		// The GUI saves a turned-off webhook with an empty URL: it must not
		// produce a delivery that fails on the missing URL.
		{"webhook off without a url", map[string]string{KeyWebhookURL: "", KeyWebhookEnabled: "false"}, []string{"email"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			svc, users, settings, notifs := newRetryTestService(t)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(srv.Close)
			if tt.settings[KeyWebhookEnabled] == "true" {
				setSettings(t, settings, map[string]string{KeyWebhookURL: srv.URL})
			}
			setSettings(t, settings, tt.settings)

			admin := &db.User{Email: "admin@test.local", DisplayName: "Admin", Role: "admin", IsActive: true}
			if err := users.Create(ctx, admin); err != nil {
				t.Fatalf("create admin: %v", err)
			}

			if err := svc.NotifyAgentOffline(ctx, uuid.New(), "web-01"); err != nil {
				t.Fatalf("NotifyAgentOffline: %v", err)
			}

			_, total, err := notifs.ListByUser(ctx, admin.ID, repositories.ListOptions{Limit: 10})
			if err != nil {
				t.Fatalf("ListByUser: %v", err)
			}
			if total != 1 {
				t.Errorf("in-app notifications = %d, want 1", total)
			}

			got := deliveryTypes(t, notifs)
			if len(got) != len(tt.want) {
				t.Fatalf("deliveries = %v, want %v", got, tt.want)
			}
			for _, w := range tt.want {
				found := false
				for _, g := range got {
					found = found || g == w
				}
				if !found {
					t.Errorf("deliveries = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestRetryDelivery_ChannelTurnedOff checks that a pending delivery whose
// channel was turned off after the event is given up without being sent.
func TestRetryDelivery_ChannelTurnedOff(t *testing.T) {
	ctx := context.Background()
	svc, users, settings, notifs := newRetryTestService(t)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	setSettings(t, settings, map[string]string{
		KeyWebhookURL:     srv.URL,
		KeyWebhookEnabled: "false",
		KeyEmailEnabled:   "false",
		// An SMTP server nobody listens on: a send attempt would fail.
		KeySMTPHost: "127.0.0.1",
		KeySMTPPort: "1",
		KeySMTPFrom: "arkeep@test.local",
	})

	admin := &db.User{Email: "admin@test.local", DisplayName: "Admin", Role: "admin", IsActive: true}
	if err := users.Create(ctx, admin); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	n := &db.Notification{UserID: admin.ID, Type: "job_failure", Title: "t", Body: "b", Payload: "{}"}
	if err := notifs.Create(ctx, n); err != nil {
		t.Fatalf("create notification: %v", err)
	}

	for _, typ := range []string{"email", "webhook"} {
		t.Run(typ, func(t *testing.T) {
			d := &db.NotificationDelivery{NotificationID: n.ID, Type: typ, Status: "pending"}
			if err := notifs.CreateDelivery(ctx, d); err != nil {
				t.Fatalf("create delivery: %v", err)
			}
			svc.retryDelivery(ctx, d)
			if d.Status != "exhausted" || d.Attempts != 0 {
				t.Errorf("status %q, attempts %d; want exhausted without an attempt", d.Status, d.Attempts)
			}
			if want := typ + " notifications are turned off"; d.LastError != want {
				t.Errorf("last error = %q, want %q", d.LastError, want)
			}
		})
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("webhook received %d requests, want none", n)
	}
}
