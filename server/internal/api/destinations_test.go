package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	proto "github.com/arkeep-io/arkeep/shared/proto"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// createDBDestination inserts a destination record directly.
func createDBDestination(t *testing.T, deps *testDeps, name, destType string) *db.Destination {
	t.Helper()
	d := &db.Destination{
		Name:        name,
		Type:        destType,
		Credentials: db.EncryptedString(`{"bucket":"test"}`),
		Config:      `{}`,
		Enabled:     true,
	}
	if err := deps.dests.Create(context.Background(), d); err != nil {
		t.Fatalf("createDBDestination: %v", err)
	}
	return d
}

func TestDestinationHandler_List(t *testing.T) {
	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/destinations", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("returns empty list on fresh DB", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/destinations", e.adminToken(t))
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

	t.Run("returns created destinations", func(t *testing.T) {
		e := newTestEnv(t)
		createDBDestination(t, e.deps, "s3-backup", "s3")
		createDBDestination(t, e.deps, "local-backup", "local")

		resp := e.get(t, "/api/v1/destinations", e.adminToken(t))
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
}

func TestDestinationHandler_Create(t *testing.T) {
	t.Run("creates destination and returns 201", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/destinations", e.adminToken(t), map[string]string{
			"name":        "my-s3-bucket",
			"type":        "s3",
			"credentials": `{"access_key":"AKIA...","secret_key":"..."}`,
			"config":      `{"bucket":"backups","region":"us-east-1"}`,
		})
		assertStatus(t, resp, http.StatusCreated)

		var data struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Type    string `json:"type"`
			Enabled bool   `json:"enabled"`
		}
		decodeData(t, resp, &data)
		if data.Name != "my-s3-bucket" {
			t.Errorf("name = %q, want my-s3-bucket", data.Name)
		}
		if data.Type != "s3" {
			t.Errorf("type = %q, want s3", data.Type)
		}
		if !data.Enabled {
			t.Error("enabled = false, want true")
		}
	})

	t.Run("returns 400 when name is missing", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/destinations", e.adminToken(t), map[string]string{
			"type": "s3",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 for invalid type", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/destinations", e.adminToken(t), map[string]string{
			"name": "dest",
			"type": "dropbox", // not in validDestinationTypes
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("accepts all valid destination types", func(t *testing.T) {
		for _, typ := range []string{"local", "s3", "sftp", "rest", "rclone"} {
			e := newTestEnv(t)
			resp := e.post(t, "/api/v1/destinations", e.adminToken(t), map[string]string{
				"name": "dest-" + typ,
				"type": typ,
			})
			assertStatus(t, resp, http.StatusCreated)
		}
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/destinations", "", map[string]string{
			"name": "dest",
			"type": "s3",
		})
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestDestinationHandler_GetByID(t *testing.T) {
	t.Run("returns destination by UUID", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "sftp-target", "sftp")

		resp := e.get(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		}
		decodeData(t, resp, &data)
		if data.ID != dest.ID.String() {
			t.Errorf("id = %q, want %q", data.ID, dest.ID.String())
		}
		if data.Type != "sftp" {
			t.Errorf("type = %q, want sftp", data.Type)
		}
	})

	t.Run("returns 404 for non-existent destination", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/destinations/00000000-0000-0000-0000-000000000001", e.adminToken(t))
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 400 for malformed UUID", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.get(t, "/api/v1/destinations/not-a-uuid", e.adminToken(t))
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("has_repo_password reflects the stored password, never the value itself", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "no-password", "rclone")

		var withoutPassword struct {
			HasRepoPassword bool `json:"has_repo_password"`
		}
		decodeData(t, e.get(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t)), &withoutPassword)
		if withoutPassword.HasRepoPassword {
			t.Error("has_repo_password = true for a destination with no stored password")
		}

		dest.RepoPassword = "imported-repo-secret"
		if err := e.deps.dests.Update(context.Background(), dest); err != nil {
			t.Fatalf("Update: %v", err)
		}

		var withPassword struct {
			HasRepoPassword bool `json:"has_repo_password"`
		}
		decodeData(t, e.get(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t)), &withPassword)
		if !withPassword.HasRepoPassword {
			t.Error("has_repo_password = false after setting a password, want true")
		}
	})
}

func TestDestinationHandler_Update(t *testing.T) {
	t.Run("updates destination name", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "old-name", "s3")

		name := "new-name"
		resp := e.patch(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t), map[string]any{
			"name": &name,
		})
		assertStatus(t, resp, http.StatusOK)

		var data struct {
			Name string `json:"name"`
		}
		decodeData(t, resp, &data)
		if data.Name != "new-name" {
			t.Errorf("name = %q, want new-name", data.Name)
		}
	})

	t.Run("returns 404 for non-existent destination", func(t *testing.T) {
		e := newTestEnv(t)
		name := "x"
		resp := e.patch(t, "/api/v1/destinations/00000000-0000-0000-0000-000000000001", e.adminToken(t), map[string]any{
			"name": &name,
		})
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("preserves credentials when PATCH omits them", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "keep-creds", "sftp")
		orig := storedCredentials(t, e, dest.ID.String())

		newName := "renamed"
		resp := e.patch(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t), map[string]any{
			"name": &newName,
		})
		assertStatus(t, resp, http.StatusOK)

		if got := storedCredentials(t, e, dest.ID.String()); got != orig {
			t.Errorf("credentials = %q, want preserved %q", got, orig)
		}
	})

	t.Run("preserves credentials when PATCH sends a blank payload", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "blank-creds", "sftp")
		orig := storedCredentials(t, e, dest.ID.String())

		blank := `{"password":"","private_key":""}`
		resp := e.patch(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t), map[string]any{
			"credentials": &blank,
		})
		assertStatus(t, resp, http.StatusOK)

		if got := storedCredentials(t, e, dest.ID.String()); got != orig {
			t.Errorf("credentials = %q, want preserved %q", got, orig)
		}
	})

	t.Run("updates credentials when PATCH sends new ones", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "update-creds", "sftp")

		newCreds := `{"password":"s3cret"}`
		resp := e.patch(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t), map[string]any{
			"credentials": &newCreds,
		})
		assertStatus(t, resp, http.StatusOK)

		if got := storedCredentials(t, e, dest.ID.String()); got != newCreds {
			t.Errorf("credentials = %q, want %q", got, newCreds)
		}
	})
}

// storedCredentials reads back the decrypted credentials of a destination.
func storedCredentials(t *testing.T, e *testEnv, id string) string {
	t.Helper()
	uid, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("storedCredentials: bad id %q: %v", id, err)
	}
	d, err := e.deps.dests.GetByID(context.Background(), uid)
	if err != nil {
		t.Fatalf("storedCredentials: %v", err)
	}
	return string(d.Credentials)
}

func TestDestinationHandler_Delete(t *testing.T) {
	t.Run("deletes destination successfully", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "to-delete", "local")

		resp := e.del(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t))
		assertStatus(t, resp, http.StatusNoContent)
	})

	t.Run("returns 404 for non-existent destination", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.del(t, "/api/v1/destinations/00000000-0000-0000-0000-000000000001", e.adminToken(t))
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.del(t, "/api/v1/destinations/00000000-0000-0000-0000-000000000001", "")
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}

// TestPersistImportedSnapshots covers importing the snapshots of a pre-existing
// Restic repository. Such snapshots belong to no policy and no job, so they are
// stored with a nil policy_id / job_id.
func TestPersistImportedSnapshots(t *testing.T) {
	newHandler := func(deps *testDeps) *DestinationHandler {
		// agentMgr is not touched by persistImportedSnapshots.
		return &DestinationHandler{
			repo:         deps.dests,
			snapshotRepo: deps.snaps,
			logger:       zap.NewNop(),
		}
	}
	result := func(ids ...string) *agentmanager.SnapshotImportResult {
		res := &agentmanager.SnapshotImportResult{}
		for _, id := range ids {
			res.Snapshots = append(res.Snapshots, &proto.ImportedSnapshotInfo{
				ResticSnapshotId: id,
				SnapshotTime:     "2026-07-26T13:20:45.123456789+02:00",
				Paths:            []string{"/data"},
				Tags:             []string{},
				Hostname:         "nas",
				SizeBytes:        2048,
				FileCount:        7,
			})
		}
		return res
	}

	t.Run("persists every snapshot found", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "imported", "rclone")
		h := newHandler(e.deps)

		out := h.persistImportedSnapshots(context.Background(), dest, result("aaa111", "bbb222"))

		if out.Imported != 2 || out.Skipped != 0 || out.Failed != 0 {
			t.Fatalf("outcome = %+v, want {Imported:2 Skipped:0 Failed:0}", out)
		}

		rows, total, err := e.deps.snaps.ListByDestination(context.Background(), dest.ID, repositories.ListOptions{Limit: 10})
		if err != nil {
			t.Fatalf("ListByDestination: %v", err)
		}
		if total != 2 {
			t.Fatalf("stored %d snapshots, want 2", total)
		}
		for _, row := range rows {
			if row.PolicyID != nil {
				t.Errorf("snapshot %s: PolicyID = %v, want nil", row.SnapshotID, row.PolicyID)
			}
			if row.JobID != nil {
				t.Errorf("snapshot %s: JobID = %v, want nil", row.SnapshotID, row.JobID)
			}
			if !row.IsImported {
				t.Errorf("snapshot %s: IsImported = false, want true", row.SnapshotID)
			}
			if row.Hostname != "nas" {
				t.Errorf("snapshot %s: Hostname = %q, want %q", row.SnapshotID, row.Hostname, "nas")
			}
			if row.SnapshotAt.IsZero() {
				t.Errorf("snapshot %s: SnapshotAt is zero, want the parsed restic timestamp", row.SnapshotID)
			}
			if row.SizeBytes != 2048 {
				t.Errorf("snapshot %s: SizeBytes = %d, want 2048", row.SnapshotID, row.SizeBytes)
			}
			if row.FileCount != 7 {
				t.Errorf("snapshot %s: FileCount = %d, want 7", row.SnapshotID, row.FileCount)
			}
		}
	})

	t.Run("reports already known snapshots as skipped, not imported", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "imported", "rclone")
		h := newHandler(e.deps)
		ctx := context.Background()

		if out := h.persistImportedSnapshots(ctx, dest, result("aaa111", "bbb222")); out.Imported != 2 {
			t.Fatalf("first import: outcome = %+v, want 2 imported", out)
		}

		out := h.persistImportedSnapshots(ctx, dest, result("aaa111", "bbb222", "ccc333"))

		if out.Imported != 1 || out.Skipped != 2 || out.Failed != 0 {
			t.Fatalf("second import: outcome = %+v, want {Imported:1 Skipped:2 Failed:0}", out)
		}
	})

	t.Run("the same repository copied to another destination is importable again", func(t *testing.T) {
		// Migrating a repository between cloud providers yields two
		// destinations holding the same restic snapshot IDs.
		e := newTestEnv(t)
		oldDest := createDBDestination(t, e.deps, "provider1", "rclone")
		newDest := createDBDestination(t, e.deps, "provider2", "rclone")
		h := newHandler(e.deps)
		ctx := context.Background()

		if out := h.persistImportedSnapshots(ctx, oldDest, result("aaa111")); out.Imported != 1 {
			t.Fatalf("import into provider1: outcome = %+v, want 1 imported", out)
		}

		out := h.persistImportedSnapshots(ctx, newDest, result("aaa111"))

		if out.Imported != 1 || out.Skipped != 0 || out.Failed != 0 {
			t.Fatalf("import into provider2: outcome = %+v, want {Imported:1 Skipped:0 Failed:0}", out)
		}
	})

	t.Run("caches the repository size reported by the agent", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "imported", "rclone")
		h := newHandler(e.deps)

		res := result("aaa111")
		res.RepoSizeBytes = 4096

		h.persistImportedSnapshots(context.Background(), dest, res)

		stored, err := e.deps.dests.GetByID(context.Background(), dest.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if stored.RepoSizeBytes != 4096 {
			t.Errorf("RepoSizeBytes = %d, want 4096", stored.RepoSizeBytes)
		}
	})
}
