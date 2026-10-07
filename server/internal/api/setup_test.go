package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

func TestSetupHandler_GetStatus(t *testing.T) {
	t.Run("returns completed=false on empty database", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/setup/status", "")
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Completed bool `json:"completed"`
		}
		decodeData(t, resp, &data)
		if data.Completed {
			t.Error("completed = true, want false on empty DB")
		}
	})

	t.Run("returns completed=true after a user exists", func(t *testing.T) {
		e := newTestEnv(t)
		createDBUser(t, e.deps, "admin@test.local", "admin")

		resp := e.get(t, "/api/v1/setup/status", "")
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Completed bool `json:"completed"`
		}
		decodeData(t, resp, &data)
		if !data.Completed {
			t.Error("completed = false, want true after user created")
		}
	})
}

func TestSetupHandler_Complete(t *testing.T) {
	t.Run("creates admin user and returns 201", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/setup/complete", "", map[string]string{
			"name":     "Admin User",
			"email":    "admin@example.com",
			"password": "secure-password-123",
		})
		assertStatus(t, resp, http.StatusCreated)
	})

	t.Run("returns 409 if setup already completed", func(t *testing.T) {
		e := newTestEnv(t)
		createDBUser(t, e.deps, "existing@example.com", "admin")

		resp := e.post(t, "/api/v1/setup/complete", "", map[string]string{
			"name":     "Second Admin",
			"email":    "second@example.com",
			"password": "password123",
		})
		assertStatus(t, resp, http.StatusConflict)
	})

	t.Run("returns 400 when name is missing", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/setup/complete", "", map[string]string{
			"email":    "admin@example.com",
			"password": "password123",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when email is missing", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/setup/complete", "", map[string]string{
			"name":     "Admin",
			"password": "password123",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when password is missing", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/setup/complete", "", map[string]string{
			"name":  "Admin",
			"email": "admin@example.com",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 on unknown fields", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/setup/complete", "", map[string]string{
			"name":     "Admin",
			"email":    "admin@example.com",
			"password": "password123",
			"unknown":  "field",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})
}

// setupRequest builds a POST /setup/complete request for direct handler calls.
func setupRequest(t *testing.T, email string) *http.Request {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": "Admin", "email": email, "password": "secure-password-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/complete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestSetupHandler_Window(t *testing.T) {
	t.Run("refuses setup after the window expires", func(t *testing.T) {
		e := newTestEnv(t)
		h := NewSetupHandler(e.deps.users, e.deps.audit, zap.NewNop())
		h.deadline = time.Now().Add(-time.Second)

		rec := httptest.NewRecorder()
		h.Complete(rec, setupRequest(t, "late@example.com"))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body: %s", rec.Code, rec.Body.String())
		}
		if _, total, _ := e.deps.users.List(context.Background(), repositories.ListOptions{Limit: 1}); total != 0 {
			t.Errorf("users = %d after an expired setup, want 0", total)
		}
	})

	t.Run("an already completed setup still answers 409 after the window", func(t *testing.T) {
		e := newTestEnv(t)
		createDBUser(t, e.deps, "existing@example.com", "admin")
		h := NewSetupHandler(e.deps.users, e.deps.audit, zap.NewNop())
		h.deadline = time.Now().Add(-time.Second)

		rec := httptest.NewRecorder()
		h.Complete(rec, setupRequest(t, "late@example.com"))
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}
	})
}

// TestSetupHandler_ConcurrentComplete guards the race where several requests
// pass the "no users yet" check together and each creates an admin.
func TestSetupHandler_ConcurrentComplete(t *testing.T) {
	e := newTestEnv(t)
	h := NewSetupHandler(e.deps.users, e.deps.audit, zap.NewNop())

	const n = 8
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.Complete(rec, setupRequest(t, fmt.Sprintf("admin%d@example.com", i)))
			codes[i] = rec.Code
		}()
	}
	wg.Wait()

	created := 0
	for _, c := range codes {
		if c == http.StatusCreated {
			created++
		}
	}
	if created != 1 {
		t.Errorf("created = %d (codes %v), want exactly 1", created, codes)
	}
	if _, total, _ := e.deps.users.List(context.Background(), repositories.ListOptions{Limit: 10}); total != 1 {
		t.Errorf("users = %d, want 1", total)
	}
}

func TestSetupHandler_CompleteIsAudited(t *testing.T) {
	e := newTestEnv(t)
	resp := e.post(t, "/api/v1/setup/complete", "", map[string]string{
		"name":     "Admin User",
		"email":    "admin@example.com",
		"password": "secure-password-123",
	})
	assertStatus(t, resp, http.StatusCreated)

	logs, total, err := e.deps.audit.List(context.Background(), repositories.AuditFilter{}, repositories.ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("audit list: %v", err)
	}
	if total != 1 || logs[0].Action != "setup.complete" || logs[0].UserEmail != "admin@example.com" {
		t.Errorf("audit = %+v (total %d), want one setup.complete by admin@example.com", logs, total)
	}
}
