package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

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
			body := map[string]string{
				"name": "dest-" + typ,
				"type": typ,
			}
			if typ == "rclone" {
				body["config"] = `{"remote":"myremote","path":"bucket"}`
			}
			resp := e.post(t, "/api/v1/destinations", e.adminToken(t), body)
			assertStatus(t, resp, http.StatusCreated)
		}
	})

	// rclone credentials used to be passed verbatim as env vars to restic on
	// the agent, so a key like RESTIC_PASSWORD_COMMAND ran a command there.
	t.Run("rejects rclone credentials", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/destinations", e.adminToken(t), map[string]string{
			"name":        "rclone-creds",
			"type":        "rclone",
			"config":      `{"remote":"myremote"}`,
			"credentials": `{"RESTIC_PASSWORD_COMMAND":"id"}`,
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("rejects an rclone connection string as remote", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/destinations", e.adminToken(t), map[string]string{
			"name":   "rclone-connstr",
			"type":   "rclone",
			"config": `{"remote":":sftp,host=evil,ssh=id:"}`,
		})
		assertStatus(t, resp, http.StatusBadRequest)
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

	t.Run("has_repo_password reflects the stored password for an s3 destination too", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "s3-no-password", "s3")

		var withoutPassword struct {
			HasRepoPassword bool `json:"has_repo_password"`
		}
		decodeData(t, e.get(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t)), &withoutPassword)
		if withoutPassword.HasRepoPassword {
			t.Error("has_repo_password = true for an s3 destination with no stored password")
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
			t.Error("has_repo_password = false after setting a password on an s3 destination, want true")
		}
	})
}

func TestDestinationHandler_CheckRepo(t *testing.T) {
	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "s3-dest", "s3")
		resp := e.post(t, "/api/v1/destinations/"+dest.ID.String()+"/check-repo", "", map[string]string{
			"agent_id":      uuid.NewString(),
			"repo_password": "secret",
		})
		assertStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("returns 404 for unknown destination", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/destinations/00000000-0000-0000-0000-000000000001/check-repo", e.adminToken(t), map[string]string{
			"agent_id":      uuid.NewString(),
			"repo_password": "secret",
		})
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 400 when agent_id missing", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "s3-dest", "s3")
		resp := e.post(t, "/api/v1/destinations/"+dest.ID.String()+"/check-repo", e.adminToken(t), map[string]string{
			"repo_password": "secret",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 when repo_password missing", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "s3-dest", "s3")
		resp := e.post(t, "/api/v1/destinations/"+dest.ID.String()+"/check-repo", e.adminToken(t), map[string]string{
			"agent_id": uuid.NewString(),
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 400 for invalid agent_id", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "s3-dest", "s3")
		resp := e.post(t, "/api/v1/destinations/"+dest.ID.String()+"/check-repo", e.adminToken(t), map[string]string{
			"agent_id":      "not-a-uuid",
			"repo_password": "secret",
		})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("returns 409 when agent is not connected", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "s3-dest", "s3")
		resp := e.post(t, "/api/v1/destinations/"+dest.ID.String()+"/check-repo", e.adminToken(t), map[string]string{
			"agent_id":      uuid.NewString(),
			"repo_password": "secret",
		})
		assertStatus(t, resp, http.StatusConflict)
	})
}

func TestDestinationHandler_Sync(t *testing.T) {
	t.Run("returns 401 without token", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "rest-dest", "rest")
		resp := e.post(t, "/api/v1/destinations/"+dest.ID.String()+"/sync", "", nil)
		assertStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("returns 403 for non-admin", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "rest-dest", "rest")
		resp := e.post(t, "/api/v1/destinations/"+dest.ID.String()+"/sync", e.userToken(t), nil)
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("returns 404 for unknown destination", func(t *testing.T) {
		e := newTestEnv(t)
		resp := e.post(t, "/api/v1/destinations/00000000-0000-0000-0000-000000000001/sync", e.adminToken(t), nil)
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("returns 409 when no agent can reach the destination", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "rest-dest", "rest")
		resp := e.post(t, "/api/v1/destinations/"+dest.ID.String()+"/sync", e.adminToken(t), nil)
		assertStatus(t, resp, http.StatusConflict)
	})
}

func TestClassifyRepoCheckError(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantStatus string
	}{
		{
			name:       "exit code 10 — no repository yet",
			raw:        "restic: command failed: exit status 10\n" + `{"message_type":"exit_error","code":10,"message":"Fatal: repository does not exist: unable to open config file"}`,
			wantStatus: "no_repo",
		},
		{
			name:       "exit code 12 — wrong password",
			raw:        "restic: command failed: exit status 12\n" + `{"message_type":"exit_error","code":12,"message":"Fatal: wrong password or no key found"}`,
			wantStatus: "wrong_password",
		},
		{
			name:       "exit code 12 without a json body still recognized via the regex fallback",
			raw:        "restic: command failed: exit status 12",
			wantStatus: "wrong_password",
		},
		{
			name:       "unrelated error",
			raw:        "restic: command failed: exit status 1\nFatal: unable to open repo: connection refused",
			wantStatus: "unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, _ := classifyRepoCheckError(tt.raw)
			if status != tt.wantStatus {
				t.Errorf("classifyRepoCheckError(%q) status = %q, want %q", tt.raw, status, tt.wantStatus)
			}
		})
	}
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

	t.Run("rejects rclone credentials and connection strings", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "legacy-rclone", "rclone")

		creds := `{"LD_PRELOAD":"/tmp/x.so"}`
		resp := e.patch(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t), map[string]any{
			"credentials": &creds,
		})
		assertStatus(t, resp, http.StatusBadRequest)

		config := `{"remote":"myremote,ssh=id:"}`
		resp = e.patch(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t), map[string]any{
			"config": &config,
		})
		assertStatus(t, resp, http.StatusBadRequest)

		// A rename does not re-validate the legacy row's stored config.
		name := "renamed"
		resp = e.patch(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t), map[string]any{
			"name": &name,
		})
		assertStatus(t, resp, http.StatusOK)
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

	t.Run("returns 403 for non-admin user", func(t *testing.T) {
		e := newTestEnv(t)
		dest := createDBDestination(t, e.deps, "protected", "local")

		resp := e.del(t, "/api/v1/destinations/"+dest.ID.String(), e.userToken(t))
		assertStatus(t, resp, http.StatusForbidden)

		if _, err := e.deps.dests.GetByID(context.Background(), dest.ID); err != nil {
			t.Errorf("destination no longer live after a forbidden delete: %v", err)
		}
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
