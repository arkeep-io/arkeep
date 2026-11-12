package api

import (
	"testing"
	"time"
)

// newTestLockout returns a lockout whose clock the test moves.
func newTestLockout() (*accountLockout, *time.Time) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	l := newAccountLockout()
	l.now = func() time.Time { return now }
	return l, &now
}

func TestAccountLockout(t *testing.T) {
	t.Run("locks after the maximum failures, for the lock duration", func(t *testing.T) {
		l, now := newTestLockout()
		for i := 1; i < maxAccountFailures; i++ {
			if l.fail("admin@test.local") {
				t.Fatalf("failure %d locked the account, want %d", i, maxAccountFailures)
			}
		}
		if _, locked := l.locked("admin@test.local"); locked {
			t.Fatal("locked before the last failure")
		}
		if !l.fail("admin@test.local") {
			t.Fatal("the last failure did not lock the account")
		}
		if wait, locked := l.locked("admin@test.local"); !locked || wait != accountLockDuration {
			t.Fatalf("locked = %v for %v, want locked for %v", locked, wait, accountLockDuration)
		}

		*now = now.Add(accountLockDuration)
		if _, locked := l.locked("admin@test.local"); locked {
			t.Error("still locked after the lock duration")
		}
		// The full number of attempts is back, not one.
		if l.fail("admin@test.local") {
			t.Error("the first failure after the lock locked the account again")
		}
	})

	t.Run("case and spaces of the email do not split the count", func(t *testing.T) {
		l, _ := newTestLockout()
		for i := 0; i < maxAccountFailures; i++ {
			email := "admin@test.local"
			if i%2 == 1 {
				email = " Admin@Test.Local "
			}
			l.fail(email)
		}
		if _, locked := l.locked("ADMIN@test.local"); !locked {
			t.Error("variants of the same email were counted apart")
		}
	})

	t.Run("failures older than the window do not add up", func(t *testing.T) {
		l, now := newTestLockout()
		for i := 1; i < maxAccountFailures; i++ {
			l.fail("admin@test.local")
		}
		*now = now.Add(accountFailureWindow + time.Second)
		if l.fail("admin@test.local") {
			t.Error("a failure after the window locked the account")
		}
	})

	t.Run("a sign-in clears the failures", func(t *testing.T) {
		l, _ := newTestLockout()
		for i := 1; i < maxAccountFailures; i++ {
			l.fail("admin@test.local")
		}
		l.reset("admin@test.local")
		if l.fail("admin@test.local") {
			t.Error("failures before the sign-in still counted")
		}
	})

	t.Run("accounts are counted apart", func(t *testing.T) {
		l, _ := newTestLockout()
		for i := 0; i < maxAccountFailures; i++ {
			l.fail("admin@test.local")
		}
		if _, locked := l.locked("other@test.local"); locked {
			t.Error("another account was locked")
		}
	})

	t.Run("expired entries are swept", func(t *testing.T) {
		l, now := newTestLockout()
		l.fail("old@test.local")
		*now = now.Add(accountFailureWindow + time.Minute)
		l.fail("new@test.local")
		if _, ok := l.accounts["old@test.local"]; ok {
			t.Error("the expired entry was kept")
		}
		if _, ok := l.accounts["new@test.local"]; !ok {
			t.Error("the live entry was dropped")
		}
	})
}
