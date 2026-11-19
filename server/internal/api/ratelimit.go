package api

import (
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// rateBucket tracks the request count for a single key within a rate-limit window.
type rateBucket struct {
	count   int
	resetAt time.Time
}

// rateLimitKey returns the bucket of a client address. An IPv6 client usually
// holds a whole /64 and can use a new address for every request, so all the
// addresses of a /64 share one bucket (S-28). An IPv4 address, or an
// IPv4-mapped IPv6 address, is a bucket of its own. Anything that does not
// parse as an address is used as it is.
func rateLimitKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	a = a.Unmap()
	if a.Is4() {
		return a.String()
	}
	return netip.PrefixFrom(a.WithZone(""), 64).Masked().String()
}

// RateLimiter is a simple fixed-window rate limiter keyed by a string: a
// client address bucket (rateLimitKey) or an account.
// It is safe for concurrent use. Call Stop to release the background goroutine.
type RateLimiter struct {
	maxRequests int
	window      time.Duration
	mu          sync.Mutex
	buckets     map[string]*rateBucket
	stop        chan struct{}
}

// NewRateLimiter creates a RateLimiter that allows at most maxRequests requests
// per key within the given window duration.
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

// Allow counts a request for key and returns true if key has not exceeded the
// rate limit. Pass a client address through rateLimitKey first.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok || now.After(b.resetAt) {
		rl.buckets[key] = &rateBucket{count: 1, resetAt: now.Add(rl.window)}
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
			if !rl.Allow(rateLimitKey(clientIP(r))) {
				w.Header().Set("Retry-After", retryAfter)
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
