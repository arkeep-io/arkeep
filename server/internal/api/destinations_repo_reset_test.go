package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// createInitializedLocalDestination creates a local destination whose
// repository is recorded as existing.
func createInitializedLocalDestination(t *testing.T, e *testEnv, path string) *db.Destination {
	t.Helper()
	ctx := context.Background()
	d := &db.Destination{Name: "usb", Type: "local", Credentials: db.EncryptedString(`{}`), Config: `{"path":"` + path + `"}`, Enabled: true}
	if err := e.deps.dests.Create(ctx, d); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	if err := e.deps.dests.MarkRepoInitialized(ctx, d.ID, time.Now().UTC()); err != nil {
		t.Fatalf("MarkRepoInitialized: %v", err)
	}
	return d
}

func repoInitializedAt(t *testing.T, e *testEnv, d *db.Destination) *time.Time {
	t.Helper()
	got, err := e.deps.dests.GetByID(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	return got.RepoInitializedAt
}

func TestDestinationHandler_ResetRepository(t *testing.T) {
	t.Run("forgets the repository and records it in the audit log", func(t *testing.T) {
		e := newTestEnv(t)
		d := createInitializedLocalDestination(t, e, "/mnt/usb")

		resp := e.post(t, "/api/v1/destinations/"+d.ID.String()+"/reset-repository", e.adminToken(t), nil)
		assertStatus(t, resp, http.StatusOK)
		var data struct {
			RepoInitializedAt string `json:"repo_initialized_at"`
		}
		decodeData(t, resp, &data)
		if data.RepoInitializedAt != "" {
			t.Errorf("repo_initialized_at = %q, want empty", data.RepoInitializedAt)
		}
		if got := repoInitializedAt(t, e, d); got != nil {
			t.Errorf("stored repo_initialized_at = %v, want nil", got)
		}

		logs, _, err := e.deps.audit.List(context.Background(), repositories.AuditFilter{}, repositories.ListOptions{Limit: 10})
		if err != nil {
			t.Fatalf("audit list: %v", err)
		}
		found := false
		for _, l := range logs {
			found = found || (l.Action == "destination.reset_repository" && l.ResourceID == d.ID.String())
		}
		if !found {
			t.Errorf("audit = %+v, want a destination.reset_repository entry", logs)
		}
	})

	t.Run("returns 404 for an unknown destination", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/destinations/00000000-0000-0000-0000-000000000001/reset-repository", e.adminToken(t), nil)
		assertStatus(t, resp, http.StatusNotFound)
	})
}

func TestDestinationHandler_UpdateKeepsRepositoryUnlessItMoves(t *testing.T) {
	t.Run("a rename keeps the repository recorded", func(t *testing.T) {
		e := newTestEnv(t)
		d := createInitializedLocalDestination(t, e, "/mnt/usb")

		resp := e.patch(t, "/api/v1/destinations/"+d.ID.String(), e.adminToken(t), map[string]any{"name": "usb-renamed"})
		assertStatus(t, resp, http.StatusOK)
		if repoInitializedAt(t, e, d) == nil {
			t.Error("repo_initialized_at cleared by a rename, want kept")
		}
	})

	t.Run("saving the same location keeps the repository recorded", func(t *testing.T) {
		e := newTestEnv(t)
		d := createInitializedLocalDestination(t, e, "/mnt/usb")

		resp := e.patch(t, "/api/v1/destinations/"+d.ID.String(), e.adminToken(t), map[string]any{"config": `{"path":"/mnt/usb"}`})
		assertStatus(t, resp, http.StatusOK)
		if repoInitializedAt(t, e, d) == nil {
			t.Error("repo_initialized_at cleared by an unchanged path, want kept")
		}
	})

	t.Run("a new location forgets the repository", func(t *testing.T) {
		e := newTestEnv(t)
		d := createInitializedLocalDestination(t, e, "/mnt/usb")

		resp := e.patch(t, "/api/v1/destinations/"+d.ID.String(), e.adminToken(t), map[string]any{"config": `{"path":"/mnt/other"}`})
		assertStatus(t, resp, http.StatusOK)
		var data struct {
			RepoInitializedAt string `json:"repo_initialized_at"`
		}
		decodeData(t, resp, &data)
		if data.RepoInitializedAt != "" || repoInitializedAt(t, e, d) != nil {
			t.Errorf("repo_initialized_at = %q after moving the repository, want cleared", data.RepoInitializedAt)
		}
	})
}
