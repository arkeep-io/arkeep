package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arkeep-io/arkeep/server/internal/notification"
)

const providersPath = "/api/v1/settings/notification-providers"

func gotifyRequest(url, token string) map[string]any {
	return map[string]any{
		"name":       "Gotify",
		"type":       "gotify",
		"enabled":    true,
		"fields":     map[string]string{"url": url, "token": token},
		"priorities": map[string]string{"job_success": "0", "job_failure": "8"},
	}
}

// TestNotificationProviders_CRUD covers create, list, update and delete, and
// that the secret fields never come back.
func TestNotificationProviders_CRUD(t *testing.T) {
	e := newTestEnv(t)
	token := e.adminToken(t)

	resp := e.post(t, providersPath, token, gotifyRequest("https://push.example.com", "AbCdEfGhIjKlMnO"))
	assertStatus(t, resp, http.StatusCreated)
	var created notificationProviderResponse
	decodeData(t, resp, &created)
	if created.Config["url"] != "https://push.example.com" || len(created.SecretsSet) != 1 || created.SecretsSet[0] != "token" {
		t.Fatalf("created = %+v", created)
	}
	if created.Priorities.JobFailure != "8" {
		t.Errorf("job_failure priority = %q, want 8", created.Priorities.JobFailure)
	}

	resp = e.get(t, providersPath, token)
	assertStatus(t, resp, http.StatusOK)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if strings.Contains(string(body), "AbCdEfGhIjKlMnO") {
		t.Fatal("list returns the token")
	}

	// A blank token keeps the stored one.
	upd := gotifyRequest("https://push2.example.com", "")
	upd["enabled"] = false
	resp = e.doJSON(t, http.MethodPut, providersPath+"/"+created.ID, token, upd)
	assertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
	stored, err := e.deps.notifProviders.List(context.Background())
	if err != nil || len(stored) != 1 {
		t.Fatalf("list stored: %v (%d)", err, len(stored))
	}
	s, err := notification.ProviderSettingsFrom(stored[0])
	if err != nil {
		t.Fatalf("decode stored: %v", err)
	}
	if s.Secrets["token"] != "AbCdEfGhIjKlMnO" || s.Config["url"] != "https://push2.example.com" || stored[0].Enabled {
		t.Errorf("after update: %+v enabled=%v", s, stored[0].Enabled)
	}

	resp = e.del(t, providersPath+"/"+created.ID, token)
	assertStatus(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()
	resp = e.del(t, providersPath+"/"+created.ID, token)
	assertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestNotificationProviders_Validation(t *testing.T) {
	e := newTestEnv(t)
	token := e.adminToken(t)

	resp := e.post(t, providersPath, token, gotifyRequest("https://push.example.com", "AbCdEfGhIjKlMnO"))
	assertStatus(t, resp, http.StatusCreated)
	var created notificationProviderResponse
	decodeData(t, resp, &created)

	discordWithPriority := map[string]any{
		"name": "Discord", "type": "discord", "enabled": true,
		"fields":     map[string]string{"webhook_url": "https://discord.com/api/webhooks/1/tok"},
		"priorities": map[string]string{"job_failure": "8"},
	}
	changedType := gotifyRequest("https://push.example.com", "")
	changedType["type"] = "ntfy"

	tests := []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{"missing token", http.MethodPost, providersPath, gotifyRequest("https://push.example.com", "")},
		{"unknown type", http.MethodPost, providersPath, map[string]any{"name": "x", "type": "fax"}},
		{"priority not supported", http.MethodPost, providersPath, discordWithPriority},
		{"type change", http.MethodPut, providersPath + "/" + created.ID, changedType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := e.doJSON(t, tt.method, tt.path, token, tt.body)
			_ = resp.Body.Close()
			assertStatus(t, resp, http.StatusBadRequest)
		})
	}
}

// TestNotificationProviders_TestUsesStoredSecret checks that a test of a
// saved provider with a blank token sends with the stored one.
func TestNotificationProviders_TestUsesStoredSecret(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.URL.Query().Get("token")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	e := newTestEnv(t)
	token := e.adminToken(t)
	resp := e.post(t, providersPath, token, gotifyRequest(srv.URL, "AbCdEfGhIjKlMnO"))
	assertStatus(t, resp, http.StatusCreated)
	var created notificationProviderResponse
	decodeData(t, resp, &created)

	req := gotifyRequest(srv.URL, "")
	req["id"] = created.ID
	resp = e.post(t, providersPath+"/test", token, req)
	assertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
	if gotToken != "AbCdEfGhIjKlMnO" {
		t.Errorf("test sent token %q, want the stored one", gotToken)
	}
}

// TestNotificationProviders_ClearOptionalSecret checks that clear_secrets
// removes a stored optional secret, which a blank value would keep.
func TestNotificationProviders_ClearOptionalSecret(t *testing.T) {
	e := newTestEnv(t)
	token := e.adminToken(t)
	ntfy := map[string]any{
		"name": "ntfy", "type": "ntfy", "enabled": true,
		"fields": map[string]string{"url": "https://ntfy.sh", "topic": "arkeep-backups", "token": "tk_secret"},
	}
	resp := e.post(t, providersPath, token, ntfy)
	assertStatus(t, resp, http.StatusCreated)
	var created notificationProviderResponse
	decodeData(t, resp, &created)

	ntfy["fields"] = map[string]string{"url": "https://ntfy.sh", "topic": "", "token": ""}
	ntfy["clear_secrets"] = []string{"token"}
	resp = e.doJSON(t, http.MethodPut, providersPath+"/"+created.ID, token, ntfy)
	assertStatus(t, resp, http.StatusOK)
	var updated notificationProviderResponse
	decodeData(t, resp, &updated)
	if len(updated.SecretsSet) != 1 || updated.SecretsSet[0] != "topic" {
		t.Errorf("secrets_set = %v, want only the kept topic", updated.SecretsSet)
	}
}
