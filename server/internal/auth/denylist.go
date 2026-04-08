package auth

import (
	"sync"
	"time"
)

// Denylist is a thread-safe in-memory set of revoked JWT IDs (JTIs).
// Entries are pruned automatically after their natural expiry time so the
// set never grows beyond the number of tokens that have been revoked within
// the current access token TTL window (15 minutes by default).
//
// Trade-off: the denylist is not persisted — server restarts clear it.
// This is acceptable because all access tokens expire within 15 minutes,
// so the worst case after a restart is a 15-minute window during which a
// token revoked just before the restart is still accepted. Refresh tokens
// are revoked in the database and are not affected by this limitation.
//
// Call Stop to release the background cleanup goroutine.
type Denylist struct {
	mu      sync.RWMutex
	entries map[string]time.Time // jti -> expiresAt
	stop    chan struct{}
}

// NewDenylist creates a Denylist and starts its background cleanup goroutine.
func NewDenylist() *Denylist {
	d := &Denylist{
		entries: make(map[string]time.Time),
		stop:    make(chan struct{}),
	}
	go d.cleanupLoop()
	return d
}

// Add revokes the token identified by jti until expiresAt.
// After expiresAt the entry is pruned automatically.
func (d *Denylist) Add(jti string, expiresAt time.Time) {
	d.mu.Lock()
	d.entries[jti] = expiresAt
	d.mu.Unlock()
}

// IsRevoked reports whether the given JTI has been explicitly revoked and
// has not yet expired. Expired entries are considered not revoked because
// the token itself would have failed signature validation first.
func (d *Denylist) IsRevoked(jti string) bool {
	d.mu.RLock()
	exp, ok := d.entries[jti]
	d.mu.RUnlock()
	return ok && time.Now().Before(exp)
}

// Stop terminates the background cleanup goroutine.
// The Denylist must not be used after Stop is called.
func (d *Denylist) Stop() {
	close(d.stop)
}

// cleanupLoop removes expired entries every 5 minutes.
func (d *Denylist) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			d.mu.Lock()
			now := time.Now()
			for jti, exp := range d.entries {
				if now.After(exp) {
					delete(d.entries, jti)
				}
			}
			d.mu.Unlock()
		case <-d.stop:
			return
		}
	}
}
