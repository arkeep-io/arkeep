package api

import (
	"strings"
	"sync"
	"time"
)

// Sign-in failures an account tolerates before it is locked, whatever
// addresses they come from (S-01, S-02). Wrong passwords and wrong second
// factors count together, so neither rotating X-Forwarded-For nor signing in
// again for a fresh two-factor challenge buys more attempts.
const (
	maxAccountFailures   = 10
	accountFailureWindow = 15 * time.Minute
	accountLockDuration  = 15 * time.Minute
)

// accountLockout counts failed sign-ins per account. It lives in memory: the
// server runs as a single replica (ADR 0005), and a restart clearing the
// counts only gives an attacker the attempts a restart takes.
//
// Accounts are keyed by the email typed at sign-in, existing or not, so a
// locked unknown address answers exactly like a locked real one.
type accountLockout struct {
	max      int
	window   time.Duration
	lockFor  time.Duration
	now      func() time.Time
	mu       sync.Mutex
	accounts map[string]*lockoutEntry
	swept    time.Time
}

type lockoutEntry struct {
	failures    int
	windowEnd   time.Time
	lockedUntil time.Time
}

func newAccountLockout() *accountLockout {
	return &accountLockout{
		max:      maxAccountFailures,
		window:   accountFailureWindow,
		lockFor:  accountLockDuration,
		now:      time.Now,
		accounts: make(map[string]*lockoutEntry),
	}
}

// accountKey normalises an email into the key of its account.
func accountKey(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// locked reports whether the account is locked and for how long still.
func (l *accountLockout) locked(email string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.accounts[accountKey(email)]
	if !ok {
		return 0, false
	}
	if wait := e.lockedUntil.Sub(l.now()); wait > 0 {
		return wait, true
	}
	return 0, false
}

// fail records a failed sign-in and reports whether it locked the account.
func (l *accountLockout) fail(email string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)

	key := accountKey(email)
	e, ok := l.accounts[key]
	if !ok || now.After(e.windowEnd) {
		e = &lockoutEntry{windowEnd: now.Add(l.window)}
		l.accounts[key] = e
	}
	e.failures++
	if e.failures < l.max {
		return false
	}
	// The lock starts a new window: once it ends the account gets the full
	// number of attempts again, not one.
	e.failures = 0
	e.lockedUntil = now.Add(l.lockFor)
	e.windowEnd = e.lockedUntil.Add(l.window)
	return true
}

// reset forgets the failures of an account that signed in.
func (l *accountLockout) reset(email string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.accounts, accountKey(email))
}

// sweep drops expired entries, at most once a minute, so failures spread
// over many addresses cannot grow the map without bound. Called with mu held.
func (l *accountLockout) sweep(now time.Time) {
	if now.Sub(l.swept) < time.Minute {
		return
	}
	l.swept = now
	for key, e := range l.accounts {
		if now.After(e.windowEnd) && now.After(e.lockedUntil) {
			delete(l.accounts, key)
		}
	}
}
