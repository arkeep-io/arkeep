package api

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/notification"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// fakeSMTP is a minimal SMTP server: no STARTTLS, AUTH PLAIN accepted. It
// records the decoded AUTH credentials and the recipients of each message.
type fakeSMTP struct {
	addr string
	mu   sync.Mutex
	auth []string
	rcpt []string
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	s := &fakeSMTP{addr: ln.Addr().String()}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

func (s *fakeSMTP) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	reply := func(line string) { _, _ = io.WriteString(conn, line+"\r\n") }
	reply("220 fake ready")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(cmd)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			reply("250-fake")
			reply("250 AUTH PLAIN")
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(cmd[len("AUTH PLAIN"):]))
			s.mu.Lock()
			s.auth = append(s.auth, string(raw))
			s.mu.Unlock()
			reply("235 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			s.mu.Lock()
			s.rcpt = append(s.rcpt, strings.Trim(cmd[len("RCPT TO:"):], "<> "))
			s.mu.Unlock()
			reply("250 ok")
		case strings.HasPrefix(upper, "DATA"):
			reply("354 go ahead")
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
			}
			reply("250 queued")
		case strings.HasPrefix(upper, "QUIT"):
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

func (s *fakeSMTP) hostPort() (string, int) {
	host, port, _ := net.SplitHostPort(s.addr)
	n := 0
	for _, c := range port {
		n = n*10 + int(c-'0')
	}
	return host, n
}

func (s *fakeSMTP) seen() (auth, rcpt []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.auth...), append([]string(nil), s.rcpt...)
}

// saveSMTP stores an SMTP configuration as the settings page does.
func saveSMTP(t *testing.T, e *testEnv, host string, port int, username, password string) {
	t.Helper()
	resp := e.doJSON(t, http.MethodPut, "/api/v1/settings/smtp", e.adminToken(t), map[string]any{
		"host": host, "port": port, "username": username, "password": password, "from": "arkeep@test.local",
	})
	assertStatus(t, resp, http.StatusOK)
}

func TestSettingsHandler_TestSMTP(t *testing.T) {
	t.Run("sends to the signed-in admin with the form values", func(t *testing.T) {
		e := newTestEnv(t)
		smtpSrv := newFakeSMTP(t)
		host, port := smtpSrv.hostPort()

		resp := e.post(t, "/api/v1/settings/smtp/test", e.adminToken(t), map[string]any{
			"host": host, "port": port, "username": "", "from": "arkeep@test.local",
		})
		assertStatus(t, resp, http.StatusOK)
		var out struct {
			SentTo string `json:"sent_to"`
		}
		decodeData(t, resp, &out)

		_, rcpt := smtpSrv.seen()
		if out.SentTo != "admin@test.local" || len(rcpt) != 1 || rcpt[0] != "admin@test.local" {
			t.Errorf("sent_to = %q, recipients = %v; want only the signed-in admin", out.SentTo, rcpt)
		}
		entries, _, err := e.deps.audit.List(context.Background(), repositories.AuditFilter{Action: "settings.smtp.test"}, repositories.ListOptions{Limit: 10})
		if err != nil || len(entries) != 1 {
			t.Errorf("audit settings.smtp.test = %d entries, %v; want 1", len(entries), err)
		}
	})

	t.Run("a blank password uses the stored one for the same server", func(t *testing.T) {
		e := newTestEnv(t)
		smtpSrv := newFakeSMTP(t)
		host, port := smtpSrv.hostPort()
		saveSMTP(t, e, host, port, "mailer", "stored-secret")

		resp := e.post(t, "/api/v1/settings/smtp/test", e.adminToken(t), map[string]any{
			"host": host, "port": port, "username": "mailer", "password": "", "from": "arkeep@test.local",
		})
		assertStatus(t, resp, http.StatusOK)
		auth, _ := smtpSrv.seen()
		if len(auth) != 1 || !strings.HasSuffix(auth[0], "\x00mailer\x00stored-secret") {
			t.Errorf("AUTH = %q, want the stored password", auth)
		}
	})

	t.Run("a blank password is refused for another server", func(t *testing.T) {
		e := newTestEnv(t)
		smtpSrv := newFakeSMTP(t)
		host, port := smtpSrv.hostPort()
		saveSMTP(t, e, "mail.example.com", 587, "mailer", "stored-secret")

		resp := e.post(t, "/api/v1/settings/smtp/test", e.adminToken(t), map[string]any{
			"host": host, "port": port, "username": "mailer", "password": "", "from": "arkeep@test.local",
		})
		assertStatus(t, resp, http.StatusBadRequest)
		if auth, rcpt := smtpSrv.seen(); len(auth)+len(rcpt) != 0 {
			t.Errorf("the stored password went to another server: auth %q", auth)
		}
	})

	t.Run("reports the delivery error", func(t *testing.T) {
		e := newTestEnv(t)
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close() // nothing listens there any more

		resp := e.post(t, "/api/v1/settings/smtp/test", e.adminToken(t), map[string]any{
			"host": "127.0.0.1", "port": port, "username": "", "from": "arkeep@test.local",
		})
		assertStatus(t, resp, http.StatusUnprocessableEntity)
	})

	t.Run("validates the form like Save", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/settings/smtp/test", e.adminToken(t), map[string]any{"host": "", "port": 25, "from": "a@b.c"})
		assertStatus(t, resp, http.StatusBadRequest)
	})
}

func TestSettingsHandler_TestWebhook(t *testing.T) {
	type received struct {
		body      []byte
		signature string
	}
	receiver := func(t *testing.T, status int) (*httptest.Server, chan received) {
		t.Helper()
		got := make(chan received, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			got <- received{body: body, signature: r.Header.Get("X-Arkeep-Signature")}
			w.WriteHeader(status)
		}))
		t.Cleanup(srv.Close)
		return srv, got
	}
	sign := func(body []byte, secret string) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		return "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}

	t.Run("posts a signed test event, with the stored secret when left blank", func(t *testing.T) {
		e := newTestEnv(t)
		if err := e.deps.settings.Set(context.Background(), notification.KeyWebhookSecret, db.EncryptedString("stored-secret")); err != nil {
			t.Fatalf("store secret: %v", err)
		}
		srv, got := receiver(t, http.StatusNoContent)

		resp := e.post(t, "/api/v1/settings/webhook/test", e.adminToken(t), map[string]any{"url": srv.URL, "secret": ""})
		assertStatus(t, resp, http.StatusOK)

		r := <-got
		var payload struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(r.body, &payload); err != nil || payload.Type != "test" {
			t.Errorf("payload type = %q (%v), want \"test\"", payload.Type, err)
		}
		if r.signature != sign(r.body, "stored-secret") {
			t.Errorf("signature = %q, want one made with the stored secret", r.signature)
		}
	})

	t.Run("a secret typed in the form signs the test", func(t *testing.T) {
		e := newTestEnv(t)
		srv, got := receiver(t, http.StatusOK)
		assertStatus(t, e.post(t, "/api/v1/settings/webhook/test", e.adminToken(t), map[string]any{"url": srv.URL, "secret": "typed"}), http.StatusOK)
		if r := <-got; r.signature != sign(r.body, "typed") {
			t.Errorf("signature = %q, want one made with the typed secret", r.signature)
		}
	})

	t.Run("reports a non-2xx answer", func(t *testing.T) {
		e := newTestEnv(t)
		srv, _ := receiver(t, http.StatusInternalServerError)
		assertStatus(t, e.post(t, "/api/v1/settings/webhook/test", e.adminToken(t), map[string]any{"url": srv.URL}), http.StatusUnprocessableEntity)
	})

	t.Run("requires an http(s) URL", func(t *testing.T) {
		e := newTestEnv(t)
		for _, u := range []string{"", "ftp://example.com"} {
			assertStatus(t, e.post(t, "/api/v1/settings/webhook/test", e.adminToken(t), map[string]any{"url": u}), http.StatusBadRequest)
		}
	})
}
