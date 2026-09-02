package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// createDBSnapshot inserts a snapshot record with real FK records to satisfy constraints.
func createDBSnapshot(t *testing.T, deps *testDeps) *db.Snapshot {
	t.Helper()
	job := createDBJob(t, deps)
	dest := createDBDestination(t, deps, "test-dest-"+uuid.NewString(), "local")
	s := &db.Snapshot{
		PolicyID:      job.PolicyID,
		DestinationID: dest.ID,
		JobID:         &job.ID,
		SnapshotID:    uuid.NewString(),
		SizeBytes:     1024,
		SnapshotAt:    time.Now(),
	}
	if err := deps.snaps.Create(context.Background(), s); err != nil {
		t.Fatalf("createDBSnapshot: %v", err)
	}
	return s
}

func TestSnapshotHandler_List(t *testing.T) {
	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/snapshots", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("returns empty list on fresh DB", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/snapshots", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		}
		decodeData(t, resp, &data)
		if data.Total != 0 {
			t.Errorf("total = %d, want 0", data.Total)
		}
	})

	t.Run("returns created snapshots", func(t *testing.T) {
		e := newTestEnv(t)
		createDBSnapshot(t, e.deps)
		createDBSnapshot(t, e.deps)

		resp := e.get(t, "/api/v1/snapshots", e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		}
		decodeData(t, resp, &data)
		if data.Total != 2 {
			t.Errorf("total = %d, want 2", data.Total)
		}
	})

	t.Run("returns 400 for invalid policy_id filter", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/snapshots?policy_id=not-a-uuid", e.adminToken(t))
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 for invalid destination_id filter", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/snapshots?destination_id=not-a-uuid", e.adminToken(t))
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("filters by policy_id", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshot(t, e.deps)
		createDBSnapshot(t, e.deps) // different policy

		resp := e.get(t, "/api/v1/snapshots?policy_id="+s.PolicyID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
		}
		decodeData(t, resp, &data)
		if data.Total != 1 {
			t.Errorf("total = %d, want 1 (filtered by policy)", data.Total)
		}
	})
}

func TestSnapshotHandler_GetByID(t *testing.T) {
	t.Run("returns snapshot by UUID", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshot(t, e.deps)

		resp := e.get(t, "/api/v1/snapshots/"+s.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			ID               string `json:"id"`
			ResticSnapshotID string `json:"restic_snapshot_id"`
		}
		decodeData(t, resp, &data)
		if data.ID != s.ID.String() {
			t.Errorf("id = %q, want %q", data.ID, s.ID.String())
		}
		if data.ResticSnapshotID != s.SnapshotID {
			t.Errorf("restic_snapshot_id = %q, want %q", data.ResticSnapshotID, s.SnapshotID)
		}
	})

	t.Run("returns 404 for non-existent snapshot", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/snapshots/00000000-0000-0000-0000-000000000001", e.adminToken(t))
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 400 for malformed UUID", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/snapshots/not-a-uuid", e.adminToken(t))
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/snapshots/00000000-0000-0000-0000-000000000001", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestSnapshotHandler_Delete(t *testing.T) {
	t.Run("admin deletes snapshot successfully", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshot(t, e.deps)

		resp := e.del(t, "/api/v1/snapshots/"+s.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusNoContent)
	})

	t.Run("returns 404 for non-existent snapshot", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.del(t, "/api/v1/snapshots/00000000-0000-0000-0000-000000000001", e.adminToken(t))
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 400 for malformed UUID", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.del(t, "/api/v1/snapshots/not-a-uuid", e.adminToken(t))
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.del(t, "/api/v1/snapshots/00000000-0000-0000-0000-000000000001", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestSnapshotHandler_Restore(t *testing.T) {
	t.Run("returns 404 for non-existent snapshot", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/snapshots/00000000-0000-0000-0000-000000000001/restore",
			e.adminToken(t), map[string]string{
				"agent_id":    uuid.NewString(),
				"target_path": "/restore/path",
			})
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 400 when agent_id is missing", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshot(t, e.deps)
		resp := e.post(t, "/api/v1/snapshots/"+s.ID.String()+"/restore",
			e.adminToken(t), map[string]string{
				"target_path": "/restore/path",
			})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when target_path is missing", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshot(t, e.deps)
		resp := e.post(t, "/api/v1/snapshots/"+s.ID.String()+"/restore",
			e.adminToken(t), map[string]string{
				"agent_id": uuid.NewString(),
			})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 for invalid agent_id", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshot(t, e.deps)
		resp := e.post(t, "/api/v1/snapshots/"+s.ID.String()+"/restore",
			e.adminToken(t), map[string]string{
				"agent_id":    "not-a-uuid",
				"target_path": "/restore/path",
			})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 for malformed snapshot UUID", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/snapshots/not-a-uuid/restore",
			e.adminToken(t), map[string]string{
				"agent_id":    uuid.NewString(),
				"target_path": "/restore/path",
			})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/snapshots/00000000-0000-0000-0000-000000000001/restore",
			"", map[string]string{
				"agent_id":    uuid.NewString(),
				"target_path": "/restore/path",
			})
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

// createDBImportedSnapshot inserts a snapshot as the import flow does: attached
// to a destination, with no policy and no job. repoPassword is stored on the
// destination when non-empty, which is what makes browse and restore possible.
func createDBImportedSnapshot(t *testing.T, deps *testDeps, repoPassword string) (*db.Snapshot, *db.Destination) {
	t.Helper()
	dest := createDBDestination(t, deps, "imported-dest-"+uuid.NewString(), "rclone")
	if repoPassword != "" {
		dest.RepoPassword = db.EncryptedString(repoPassword)
		if err := deps.dests.Update(context.Background(), dest); err != nil {
			t.Fatalf("createDBImportedSnapshot: store repo password: %v", err)
		}
	}
	s := &db.Snapshot{
		DestinationID: dest.ID,
		IsImported:    true,
		SnapshotID:    uuid.NewString(),
		Sources:       `["/data"]`,
		Tags:          `[]`,
		SnapshotAt:    time.Now(),
	}
	if err := deps.snaps.Create(context.Background(), s); err != nil {
		t.Fatalf("createDBImportedSnapshot: %v", err)
	}
	return s, dest
}

func TestSnapshotHandler_RestoreImported(t *testing.T) {
	t.Run("returns 422 when the destination has no stored repository password", func(t *testing.T) {
		e := newTestEnv(t)
		s, _ := createDBImportedSnapshot(t, e.deps, "")
		agent := createDBAgent(t, e.deps, "restore-agent")

		resp := e.post(t, "/api/v1/snapshots/"+s.ID.String()+"/restore",
			e.adminToken(t), map[string]string{
				"agent_id":    agent.ID.String(),
				"target_path": "/restore/path",
			})

		assertStatus(t, resp, http.StatusUnprocessableEntity)
		// Guard against the status matching for an unrelated reason.
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if !strings.Contains(string(body), "repository password") {
			t.Errorf("body = %s, want it to mention the missing repository password", strings.TrimSpace(string(body)))
		}
	})

	t.Run("creates a restore job with no policy", func(t *testing.T) {
		e := newTestEnv(t)
		s, _ := createDBImportedSnapshot(t, e.deps, "repo-secret")
		agent := createDBAgent(t, e.deps, "restore-agent")

		// The agent is not connected, so the job is queued (202) rather than
		// dispatched immediately — but it must already have been persisted by
		// then, which is what proves a policy-less restore job is storable.
		resp := e.post(t, "/api/v1/snapshots/"+s.ID.String()+"/restore",
			e.adminToken(t), map[string]string{
				"agent_id":    agent.ID.String(),
				"target_path": "/restore/path",
			})
		assertStatus(t, resp, http.StatusAccepted)

		var jobs []db.Job
		if err := e.deps.gdb.Where("agent_id = ?", agent.ID).Find(&jobs).Error; err != nil {
			t.Fatalf("load jobs: %v", err)
		}
		if len(jobs) != 1 {
			t.Fatalf("stored %d jobs, want 1", len(jobs))
		}
		if jobs[0].PolicyID != nil {
			t.Errorf("job PolicyID = %v, want nil", jobs[0].PolicyID)
		}
		if jobs[0].Type != "restore" {
			t.Errorf("job Type = %q, want %q", jobs[0].Type, "restore")
		}
	})
}

func TestSnapshotHandler_BrowseImported(t *testing.T) {
	t.Run("returns 400 when agent_id is not supplied", func(t *testing.T) {
		e := newTestEnv(t)
		s, _ := createDBImportedSnapshot(t, e.deps, "repo-secret")

		resp := e.get(t, "/api/v1/snapshots/"+s.ID.String()+"/browse", e.adminToken(t))

		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 422 when the destination has no stored repository password", func(t *testing.T) {
		e := newTestEnv(t)
		s, _ := createDBImportedSnapshot(t, e.deps, "")
		agent := createDBAgent(t, e.deps, "browse-agent")

		resp := e.get(t, "/api/v1/snapshots/"+s.ID.String()+"/browse?agent_id="+agent.ID.String(), e.adminToken(t))

		assertStatus(t, resp, http.StatusUnprocessableEntity)
	})

	t.Run("returns 503 when the chosen agent is offline", func(t *testing.T) {
		e := newTestEnv(t)
		s, _ := createDBImportedSnapshot(t, e.deps, "repo-secret")
		agent := createDBAgent(t, e.deps, "browse-agent")

		resp := e.get(t, "/api/v1/snapshots/"+s.ID.String()+"/browse?agent_id="+agent.ID.String(), e.adminToken(t))

		assertStatus(t, resp, http.StatusServiceUnavailable)
	})
}
