package auth

import (
	"testing"
	"time"
)

// TestArgon2ConcurrencyIsBounded guards against a burst of password checks
// allocating 64 MiB each without limit (SEC-20): once every slot is taken, a
// further hash waits for one to be released.
func TestArgon2ConcurrencyIsBounded(t *testing.T) {
	for range cap(argon2Slots) {
		argon2Slots <- struct{}{}
	}

	done := make(chan struct{})
	go func() {
		_, _ = HashPassword("password")
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("HashPassword ran while every Argon2 slot was taken")
	case <-time.After(100 * time.Millisecond):
	}

	<-argon2Slots
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("HashPassword did not run after a slot was released")
	}
	for range cap(argon2Slots) - 1 {
		<-argon2Slots
	}
}
