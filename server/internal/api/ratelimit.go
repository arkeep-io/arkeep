package api

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// rateBucket tracks the request count for a single IP within a rate-limit window.
type rateBucket struct {
	count   int
	resetAt time.Time
}

// RateLimiter is a simple fixed-window per-IP rate limiter.
// It is safe for concurrent use. Call Stop to release the background goroutine.
type RateLimiter struct {
	maxRequests int
	window      time.Duration
	mu          sync.Mutex
	buckets     map[string]*rateBucket
	stop        chan struct{}
}

// NewRateLimiter creates a RateLimiter that allows at most maxRequests requests
// per IP within the given window duration.
func NewRateLimiter(maxRequests int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		maxRequests: maxRequests,
		window:      window,
		buckets:     make(map[string]*rateBucket),
		stop:        make(chan struct{}),
	}
	go rl.cleanupLoop()
	return rl
}

// Stop terminates the background cleanup goroutine. The RateLimiter must not
// be used after Stop is called.
func (rl *RateLimiter) Stop() {
	close(rl.stop)
}

// Allow returns true if the given IP has not exceeded the rate limit.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[ip]
	if !ok || now.After(b.resetAt) {
		rl.buckets[ip] = &rateBucket{count: 1, resetAt: now.Add(rl.window)}
		return true
	}
	if b.count >= rl.maxRequests {
		return false
	}
	b.count++
	return true
}

// cleanupLoop removes expired buckets periodically to avoid unbounded memory growth.
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.window)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for ip, b := range rl.buckets {
				if now.After(b.resetAt) {
					delete(rl.buckets, ip)
				}
			}
			rl.mu.Unlock()
		case <-rl.stop:
			return
		}
	}
}

// RateLimit returns a Chi-compatible middleware that enforces the given rate
// limiter. Requests that exceed the limit receive 429 Too Many Requests with
// a Retry-After header indicating how long to wait (in seconds).
func RateLimit(rl *RateLimiter) func(http.Handler) http.Handler {
	retryAfter := strconv.Itoa(int(rl.window.Seconds()))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !rl.Allow(clientIP(r)) {
				w.Header().Set("Retry-After", retryAfter)
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
