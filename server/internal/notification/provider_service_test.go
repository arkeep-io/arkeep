package notification

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// gotifyProvider returns a Gotify provider that posts to serverURL.
func gotifyProvider(t *testing.T, serverURL string, enabled bool) *db.NotificationProvider {
	t.Helper()
	cfg, _ := json.Marshal(map[string]string{"url": serverURL})
	sec, _ := json.Marshal(map[string]string{"token": "AbCdEfGhIjKlMnO"})
	return &db.NotificationProvider{
		Name: "gotify", Type: ProviderGotify, Config: string(cfg), Secrets: db.EncryptedString(sec),
		Enabled: enabled, PriorityAgentOffline: "8",
	}
}

// TestNotify_SendsToEnabledProviders checks that an event goes to every
// enabled provider, with the priority set for its type, and is recorded as a
// "provider" delivery naming the provider.
func TestNotify_SendsToEnabledProviders(t *testing.T) {
	ctx := context.Background()
	svc, gdb := newTestService(t)
	users := repositories.NewUserRepository(gdb)
	providers := repositories.NewNotificationProviderRepository(gdb)
	notifs := repositories.NewNotificationRepository(gdb)

	var mu sync.Mutex
	var priorities []float64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Priority float64 `json:"priority"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		priorities = append(priorities, body.Priority)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	if err := users.Create(ctx, &db.User{Email: "admin@test.local", DisplayName: "Admin", Role: "admin", IsActive: true}); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	enabled := gotifyProvider(t, srv.URL, true)
	if err := providers.Create(ctx, enabled); err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if err := providers.Create(ctx, gotifyProvider(t, srv.URL, false)); err != nil {
		t.Fatalf("create disabled provider: %v", err)
	}

	if err := svc.NotifyAgentOffline(ctx, uuid.Must(uuid.NewV7()), "web-01"); err != nil {
		t.Fatalf("NotifyAgentOffline: %v", err)
	}

	if len(priorities) != 1 || priorities[0] != 8 {
		t.Fatalf("provider requests with priorities %v, want one with priority 8", priorities)
	}
	sent, _, err := notifs.ListDeliveriesByStatus(ctx, "sent", repositories.ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	var found bool
	for _, d := range sent {
		if d.Type == "provider" {
			found = d.ProviderID != nil && *d.ProviderID == enabled.ID
		}
	}
	if !found {
		t.Errorf("no sent provider delivery for the enabled provider in %+v", sent)
	}
}

// TestRetryDelivery_ProviderDisabled checks that a pending delivery to a
// provider disabled after the event is given up instead of sent.
func TestRetryDelivery_ProviderDisabled(t *testing.T) {
	ctx := context.Background()
	svc, gdb := newTestService(t)
	users := repositories.NewUserRepository(gdb)
	providers := repositories.NewNotificationProviderRepository(gdb)
	notifs := repositories.NewNotificationRepository(gdb)

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++ }))
	t.Cleanup(srv.Close)

	admin := &db.User{Email: "admin@test.local", DisplayName: "Admin", Role: "admin", IsActive: true}
	if err := users.Create(ctx, admin); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	p := gotifyProvider(t, srv.URL, true)
	if err := providers.Create(ctx, p); err != nil {
		t.Fatalf("create provider: %v", err)
	}
	n := &db.Notification{UserID: admin.ID, Type: "job_failure", Title: "t", Body: "b", Payload: "{}"}
	if err := notifs.Create(ctx, n); err != nil {
		t.Fatalf("create notification: %v", err)
	}
	d := &db.NotificationDelivery{NotificationID: n.ID, Type: "provider", Status: "pending", ProviderID: &p.ID}
	if err := notifs.CreateDelivery(ctx, d); err != nil {
		t.Fatalf("create delivery: %v", err)
	}

	p.Enabled = false
	if err := providers.Update(ctx, p); err != nil {
		t.Fatalf("disable provider: %v", err)
	}
	svc.retryDelivery(ctx, d)

	if d.Status != "exhausted" || calls != 0 {
		t.Errorf("status %q after %d requests; want exhausted without sending", d.Status, calls)
	}
}
