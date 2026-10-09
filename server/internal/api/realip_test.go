package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestParseTrustedProxies(t *testing.T) {
	if p, err := ParseTrustedProxies(""); err != nil || len(p) != len(DefaultTrustedProxies) {
		t.Errorf("empty spec = %v, %v; want the default private ranges", p, err)
	}
	if p, err := ParseTrustedProxies("none"); err != nil || len(p) != 0 {
		t.Errorf("none = %v, %v; want no trusted proxy", p, err)
	}
	if p, err := ParseTrustedProxies(" 10.0.0.0/8, 203.0.113.7 ,::1 "); err != nil || len(p) != 3 {
		t.Errorf("list = %v, %v; want 3 prefixes", p, err)
	}
	if _, err := ParseTrustedProxies("10.0.0.0/8,not-an-ip"); err == nil {
		t.Error("an invalid entry was accepted")
	}
}

// TestRealIP covers SEC-19: behind a reverse proxy every client used to share
// the proxy's address, so one client's failed logins locked everyone out.
func TestRealIP(t *testing.T) {
	trusted, err := ParseTrustedProxies("")
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}
	tests := []struct {
		name, remote string
		headers      map[string]string
		want         string
	}{
		{"direct client, no header", "203.0.113.5:4000", nil, "203.0.113.5"},
		{"untrusted peer cannot spoof", "203.0.113.5:4000", map[string]string{"X-Forwarded-For": "1.2.3.4"}, "203.0.113.5"},
		{"trusted proxy, single hop", "172.18.0.2:4000", map[string]string{"X-Forwarded-For": "198.51.100.9"}, "198.51.100.9"},
		{"rightmost untrusted wins over a spoofed left entry", "172.18.0.2:4000", map[string]string{"X-Forwarded-For": "1.2.3.4, 198.51.100.9"}, "198.51.100.9"},
		{"chained trusted proxies are skipped", "127.0.0.1:4000", map[string]string{"X-Forwarded-For": "198.51.100.9, 10.0.0.3"}, "198.51.100.9"},
		{"all hops private", "172.18.0.2:4000", map[string]string{"X-Forwarded-For": "192.168.1.50"}, "192.168.1.50"},
		{"X-Real-IP fallback", "172.18.0.2:4000", map[string]string{"X-Real-IP": "198.51.100.9"}, "198.51.100.9"},
		{"garbage header keeps the peer", "172.18.0.2:4000", map[string]string{"X-Forwarded-For": "garbage"}, "172.18.0.2"},
		{"IPv6 client", "[::1]:4000", map[string]string{"X-Forwarded-For": "2001:db8::7"}, "2001:db8::7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			h := RealIP(trusted, zap.NewNop())(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = clientIP(r)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remote
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != tt.want {
				t.Errorf("client IP = %q, want %q", got, tt.want)
			}
		})
	}
}
