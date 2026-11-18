package api

import "testing"

// TestAPIResponsesAreNotCached checks that every /api/v1 response, public or
// authenticated, successful or not, tells browsers and proxies not to store
// it. A cached copy of GET /auth/password-reset/status taken before SMTP was
// configured kept the forgot-password page saying SMTP was missing (#447).
func TestAPIResponsesAreNotCached(t *testing.T) {
	e := newTestEnv(t)
	admin := e.adminToken(t)

	tests := []struct {
		name  string
		path  string
		token string
	}{
		{"public endpoint", "/api/v1/auth/password-reset/status", ""},
		{"authenticated endpoint", "/api/v1/settings/smtp", admin},
		{"unauthenticated request", "/api/v1/settings/smtp", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := e.get(t, tt.path, tt.token)
			_ = resp.Body.Close()
			if got := resp.Header.Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want %q", got, "no-store")
			}
		})
	}

	t.Run("health endpoint is not affected", func(t *testing.T) {
		resp := e.get(t, "/health/live", "")
		_ = resp.Body.Close()
		if got := resp.Header.Get("Cache-Control"); got != "" {
			t.Errorf("Cache-Control = %q, want none outside /api/v1", got)
		}
	})
}
