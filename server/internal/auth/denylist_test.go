package auth

import (
	"sync"
	"testing"
	"time"
)

func TestDenylist_IsRevoked(t *testing.T) {
	t.Run("fresh entry is revoked", func(t *testing.T) {
		d := NewDenylist()
		t.Cleanup(d.Stop)

		d.Add("jti-1", time.Now().Add(time.Minute))
		if !d.IsRevoked("jti-1") {
			t.Error("expected jti-1 to be revoked")
		}
	})

	t.Run("unknown JTI is not revoked", func(t *testing.T) {
		d := NewDenylist()
		t.Cleanup(d.Stop)

		if d.IsRevoked("unknown") {
			t.Error("unknown JTI should not be revoked")
		}
	})

	t.Run("expired entry is no longer revoked", func(t *testing.T) {
		d := NewDenylist()
		t.Cleanup(d.Stop)

		d.Add("jti-exp", time.Now().Add(-time.Second))
		if d.IsRevoked("jti-exp") {
			t.Error("expired entry should not be considered revoked")
		}
	})

	t.Run("different JTIs are independent", func(t *testing.T) {
		d := NewDenylist()
		t.Cleanup(d.Stop)

		d.Add("jti-a", time.Now().Add(time.Minute))

		if !d.IsRevoked("jti-a") {
			t.Error("jti-a should be revoked")
		}
		if d.IsRevoked("jti-b") {
			t.Error("jti-b should not be revoked")
		}
	})
}

func TestDenylist_IsRevoked_Concurrent(t *testing.T) {
	d := NewDenylist()
	t.Cleanup(d.Stop)

	const n = 100
	var wg sync.WaitGroup

	// Concurrent writers.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			jti := "jti-" + string(rune('a'+i%26))
			d.Add(jti, time.Now().Add(time.Minute))
		}(i)
	}

	// Concurrent readers.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			jti := "jti-" + string(rune('a'+i%26))
			d.IsRevoked(jti) //nolint:errcheck
		}(i)
	}

	wg.Wait()
}
