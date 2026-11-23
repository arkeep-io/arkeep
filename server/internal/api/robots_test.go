package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"
)

// TestResponsesAreNotIndexed checks that every response, API and GUI alike,
// tells search engines not to index it, and that the GUI serves robots.txt.
func TestResponsesAreNotIndexed(t *testing.T) {
	const want = "noindex, nofollow"

	e := newTestEnv(t)
	for _, tt := range []struct{ name, path string }{
		{"API endpoint", "/api/v1/auth/password-reset/status"},
		{"unauthenticated API request", "/api/v1/settings/smtp"},
		{"health endpoint", "/health/live"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resp := e.get(t, tt.path, "")
			_ = resp.Body.Close()
			if got := resp.Header.Get("X-Robots-Tag"); got != want {
				t.Errorf("X-Robots-Tag = %q, want %q", got, want)
			}
		})
	}

	// The GUI is mounted after the router's middleware, as in main.go.
	r := chi.NewRouter()
	r.Use(SecurityHeaders)
	robots := "User-agent: *\nDisallow: /\n"
	MountGUI(r, fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html>")},
		"robots.txt": {Data: []byte(robots)},
	})
	gui := httptest.NewServer(r)
	t.Cleanup(gui.Close)

	for _, tt := range []struct{ name, path, body string }{
		{"GUI entrypoint", "/", "<!doctype html>"},
		{"SPA route", "/agents", "<!doctype html>"},
		{"robots.txt", "/robots.txt", robots},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(gui.URL + tt.path)
			if err != nil {
				t.Fatalf("GET %s: %v", tt.path, err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if resp.StatusCode != http.StatusOK || string(body) != tt.body {
				t.Errorf("GET %s = %d %q, want 200 %q", tt.path, resp.StatusCode, body, tt.body)
			}
			if got := resp.Header.Get("X-Robots-Tag"); got != want {
				t.Errorf("X-Robots-Tag = %q, want %q", got, want)
			}
		})
	}
}
