package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
)

// newProxiedTestEnv returns a test server that trusts X-Forwarded-For from
// loopback, where the test client connects from: each request can claim its
// own client address, as an attacker behind a trusted network can (S-01).
func newProxiedTestEnv(t *testing.T) *testEnv {
	t.Helper()
	return newTestEnvWith(t, func(cfg *RouterConfig) {
		cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	})
}

// clientIPs hands out a different client address for every request, so the
// per-address rate limits never apply.
type clientIPs struct{ n atomic.Int32 }

func (c *clientIPs) next() string {
	n := c.n.Add(1)
	return fmt.Sprintf("198.51.100.%d", n%250+1)
}

// postFrom posts body as the client at ip.
func (e *testEnv) postFrom(t *testing.T, path, ip string, body any) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("postFrom marshal: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, e.URL+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", ip)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Errorf("POST %s: %v", path, err)
		return nil
	}
	return resp
}

func (e *testEnv) loginFrom(t *testing.T, ip, email, password string) *http.Response {
	t.Helper()
	return e.postFrom(t, "/api/v1/auth/login", ip, map[string]string{"email": email, "password": password})
}

// challengeFrom signs in with the right password and returns the challenge.
func (e *testEnv) challengeFrom(t *testing.T, ip, email string) string {
	t.Helper()
	resp := e.loginFrom(t, ip, email, "test-password-123")
	assertStatus(t, resp, http.StatusOK)
	var out loginStepOneResult
	decodeData(t, resp, &out)
	if out.ChallengeToken == "" {
		t.Fatal("no challenge token")
	}
	return out.ChallengeToken
}

func (e *testEnv) codeFrom(t *testing.T, ip, challenge, code string) *http.Response {
	t.Helper()
	return e.postFrom(t, "/api/v1/auth/login/2fa", ip, map[string]string{"challenge_token": challenge, "code": code})
}

// TestLogin_AccountLockout covers S-01 and S-02: failed sign-ins are counted
// per account, so changing the client address or signing in again for a
// fresh two-factor challenge does not buy more attempts.
func TestLogin_AccountLockout(t *testing.T) {
	t.Run("wrong passwords from rotating addresses lock the account", func(t *testing.T) {
		e := newProxiedTestEnv(t)
		createDBUser(t, e.deps, "admin@test.local", "admin")
		var ips clientIPs

		for i := 0; i < maxAccountFailures; i++ {
			assertStatus(t, e.loginFrom(t, ips.next(), "admin@test.local", "wrong"), http.StatusUnauthorized)
		}
		// Locked: even the right password is refused, without saying whether
		// it was right.
		resp := e.loginFrom(t, ips.next(), "admin@test.local", "test-password-123")
		assertStatus(t, resp, http.StatusTooManyRequests)
		if resp.Header.Get("Retry-After") == "" {
			t.Error("no Retry-After on a locked account")
		}
	})

	t.Run("an unknown email locks like a real one", func(t *testing.T) {
		e := newProxiedTestEnv(t)
		var ips clientIPs
		for i := 0; i < maxAccountFailures; i++ {
			assertStatus(t, e.loginFrom(t, ips.next(), "nobody@test.local", "wrong"), http.StatusUnauthorized)
		}
		assertStatus(t, e.loginFrom(t, ips.next(), "nobody@test.local", "wrong"), http.StatusTooManyRequests)
	})

	t.Run("a successful sign-in clears the failures", func(t *testing.T) {
		e := newProxiedTestEnv(t)
		createDBUser(t, e.deps, "admin@test.local", "admin")
		var ips clientIPs

		for i := 1; i < maxAccountFailures; i++ {
			assertStatus(t, e.loginFrom(t, ips.next(), "admin@test.local", "wrong"), http.StatusUnauthorized)
		}
		assertStatus(t, e.loginFrom(t, ips.next(), "admin@test.local", "test-password-123"), http.StatusOK)
		for i := 1; i < maxAccountFailures; i++ {
			assertStatus(t, e.loginFrom(t, ips.next(), "admin@test.local", "wrong"), http.StatusUnauthorized)
		}
		assertStatus(t, e.loginFrom(t, ips.next(), "admin@test.local", "test-password-123"), http.StatusOK)
	})

	t.Run("wrong codes over fresh challenges lock the account", func(t *testing.T) {
		e := newProxiedTestEnv(t)
		enable2FA(t, e.deps, createDBUser(t, e.deps, "tfa@test.local", "admin"))
		var ips clientIPs

		// Four wrong codes per challenge stay under its own limit of five:
		// only the per-account count can stop this.
		failures := 0
		for failures < maxAccountFailures {
			challenge := e.challengeFrom(t, ips.next(), "tfa@test.local")
			for i := 0; i < maxTwoFactorAttempts-1 && failures < maxAccountFailures; i++ {
				assertStatus(t, e.codeFrom(t, ips.next(), challenge, "000000"), http.StatusBadRequest)
				failures++
			}
		}

		assertStatus(t, e.loginFrom(t, ips.next(), "tfa@test.local", "test-password-123"), http.StatusTooManyRequests)

		// A challenge obtained before the lock does not get around it either.
		e2 := newProxiedTestEnv(t)
		secret := enable2FA(t, e2.deps, createDBUser(t, e2.deps, "tfa@test.local", "admin"))
		challenge := e2.challengeFrom(t, ips.next(), "tfa@test.local")
		for i := 0; i < maxAccountFailures; i++ {
			e2.loginFrom(t, ips.next(), "tfa@test.local", "wrong")
		}
		assertStatus(t, e2.codeFrom(t, ips.next(), challenge, currentCode(t, secret)), http.StatusTooManyRequests)
	})
}

// TestLoginTwoFactor_Concurrent covers the race of S-02: the attempts of a
// challenge are counted atomically, and a challenge yields one session only.
func TestLoginTwoFactor_Concurrent(t *testing.T) {
	t.Run("parallel wrong codes use up the challenge", func(t *testing.T) {
		e := newProxiedTestEnv(t)
		id := createDBUser(t, e.deps, "tfa@test.local", "admin")
		secret := enable2FA(t, e.deps, id)
		var ips clientIPs
		challenge := e.challengeFrom(t, ips.next(), "tfa@test.local")

		// More than the challenge allows, fewer than lock the account.
		n := maxTwoFactorAttempts + 3
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			ip := ips.next()
			wg.Go(func() {
				if resp := e.codeFrom(t, ip, challenge, "000000"); resp != nil {
					_ = resp.Body.Close()
				}
			})
		}
		wg.Wait()

		assertStatus(t, e.codeFrom(t, ips.next(), challenge, currentCode(t, secret)), http.StatusBadRequest)
	})

	t.Run("parallel right codes issue one session", func(t *testing.T) {
		e := newProxiedTestEnv(t)
		id := createDBUser(t, e.deps, "tfa@test.local", "admin")
		secret := enable2FA(t, e.deps, id)
		var ips clientIPs
		challenge := e.challengeFrom(t, ips.next(), "tfa@test.local")
		code := currentCode(t, secret)

		var ok atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 5; i++ {
			ip := ips.next()
			wg.Go(func() {
				resp := e.codeFrom(t, ip, challenge, code)
				if resp == nil {
					return
				}
				if resp.StatusCode == http.StatusOK {
					ok.Add(1)
				}
				_ = resp.Body.Close()
			})
		}
		wg.Wait()
		if got := ok.Load(); got != 1 {
			t.Errorf("%d sessions issued for one challenge, want 1", got)
		}
	})
}
