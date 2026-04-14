package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// createDBSnapshot inserts a snapshot record directly.
func createDBSnapshot(t *testing.T, deps *testDeps) *db.Snapshot {
	t.Helper()
	s := &db.Snapshot{
		PolicyID:      uuid.New(),
		DestinationID: uuid.New(),
		JobID:         uuid.New(),
		SnapshotID:    "abc123",
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
		if data.ResticSnapshotID != "abc123" {
			t.Errorf("restic_snapshot_id = %q, want abc123", data.ResticSnapshotID)
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
