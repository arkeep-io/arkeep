package api

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestRateLimiter_Allow tests the core Allow method in isolation.
func TestRateLimiter_Allow(t *testing.T) {
	t.Run("allows requests up to the limit", func(t *testing.T) {
		rl := NewRateLimiter(3, time.Minute)
		for i := 0; i < 3; i++ {
			if !rl.Allow("1.2.3.4") {
				t.Fatalf("request %d should be allowed", i+1)
			}
		}
	})

	t.Run("blocks once limit is exceeded", func(t *testing.T) {
		rl := NewRateLimiter(3, time.Minute)
		for i := 0; i < 3; i++ {
			rl.Allow("1.2.3.4") //nolint:errcheck
		}
		if rl.Allow("1.2.3.4") {
			t.Fatal("4th request should be blocked")
		}
	})

	t.Run("different IPs are independent", func(t *testing.T) {
		rl := NewRateLimiter(1, time.Minute)
		if !rl.Allow("10.0.0.1") {
			t.Fatal("first IP first request should be allowed")
		}
		if rl.Allow("10.0.0.1") {
			t.Fatal("first IP second request should be blocked")
		}
		if !rl.Allow("10.0.0.2") {
			t.Fatal("second IP should not be affected by first IP's limit")
		}
	})

	t.Run("resets after window expires", func(t *testing.T) {
		rl := NewRateLimiter(1, 50*time.Millisecond)
		if !rl.Allow("1.2.3.4") {
			t.Fatal("first request should be allowed")
		}
		if rl.Allow("1.2.3.4") {
			t.Fatal("second request within window should be blocked")
		}
		time.Sleep(60 * time.Millisecond)
		if !rl.Allow("1.2.3.4") {
			t.Fatal("first request after window reset should be allowed")
		}
	})
}

// TestRateLimiter_Allow_Concurrent verifies that concurrent calls never allow
// more requests than the configured limit for a single IP.
func TestRateLimiter_Allow_Concurrent(t *testing.T) {
	const limit = 10
	rl := NewRateLimiter(limit, time.Minute)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rl.Allow("10.0.0.1") {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if allowed != limit {
		t.Errorf("allowed %d requests concurrently, want exactly %d", allowed, limit)
	}
}

// TestRateLimit_Middleware tests the Chi middleware wrapper.
func TestRateLimit_Middleware(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("passes allowed request to next handler", func(t *testing.T) {
		handler := RateLimit(NewRateLimiter(5, time.Minute))(next)

		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = "1.2.3.4:5678"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("returns 429 when limit exceeded", func(t *testing.T) {
		handler := RateLimit(NewRateLimiter(2, time.Minute))(next)

		ip := "5.6.7.8"
		for i := 0; i < 2; i++ {
			req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
			req.RemoteAddr = ip + ":1234"
			handler.ServeHTTP(httptest.NewRecorder(), req)
		}

		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusTooManyRequests {
			t.Errorf("status = %d, want %d", w.Code, http.StatusTooManyRequests)
		}
	})

	t.Run("blocked response includes Retry-After header", func(t *testing.T) {
		handler := RateLimit(NewRateLimiter(1, 2*time.Minute))(next)

		ip := "9.9.9.9:1234"
		// First request: allowed, exhausts the limit.
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = ip
		handler.ServeHTTP(httptest.NewRecorder(), req)

		// Second request: blocked — must carry Retry-After.
		req = httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = ip
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusTooManyRequests {
			t.Errorf("status = %d, want %d", w.Code, http.StatusTooManyRequests)
		}
		if got, want := w.Header().Get("Retry-After"), "120"; got != want {
			t.Errorf("Retry-After = %q, want %q", got, want)
		}
	})

	t.Run("different IPs do not interfere via middleware", func(t *testing.T) {
		handler := RateLimit(NewRateLimiter(1, time.Minute))(next)

		// Exhaust limit for first IP.
		for i := 0; i < 2; i++ {
			req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
			req.RemoteAddr = "1.1.1.1:1234"
			handler.ServeHTTP(httptest.NewRecorder(), req)
		}

		// Second IP should still be allowed.
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = "2.2.2.2:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("unrelated IP: status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}
