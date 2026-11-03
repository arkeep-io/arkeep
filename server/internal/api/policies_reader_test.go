package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestPolicyHandler_ReaderDoesNotSeeCommands pins what the read-only user role
// may read of a policy: hooks and command-source commands often carry database
// passwords, and the Healthchecks.io ping URL is the credential that reports a
// backup's outcome. An admin still reads everything, to edit it.
func TestPolicyHandler_ReaderDoesNotSeeCommands(t *testing.T) {
	const (
		hook        = `{"name":"dump","command":"PGPASSWORD=hunter2 pg_dump db","args":[],"timeout_secs":30}`
		pingURL     = "https://hc-ping.com/11111111-2222-3333-4444-555555555555"
		sourcesJSON = `[{"type":"directory","path":"/data"},{"type":"command","path":"MYSQL_PWD=s3cret mysqldump app","label":"app-db"},{"TYPE":"command","PATH":"PGPASSWORD=hunter2 pg_dump other","label":"other-db"}]`
	)
	e := newTestEnv(t)
	agentID := createDBAgent(t, e.deps, "test-agent").ID
	p := createDBPolicy(t, e.deps, "db-backup", agentID)
	p.HookPreBackup, p.HookPostBackup, p.HealthcheckURL, p.Sources = hook, hook, pingURL, sourcesJSON
	if err := e.deps.policies.Update(context.Background(), p); err != nil {
		t.Fatalf("Update policy: %v", err)
	}

	type view struct {
		HookPreBackup  string `json:"hook_pre_backup"`
		HookPostBackup string `json:"hook_post_backup"`
		HealthcheckURL string `json:"healthcheck_url"`
		Sources        string `json:"sources"`
	}
	read := func(token string) (single, listed view) {
		t.Helper()
		resp := e.get(t, "/api/v1/policies/"+p.ID.String(), token)
		assertStatus(t, resp, http.StatusOK)
		decodeData(t, resp, &single)
		var list struct {
			Items []view `json:"items"`
		}
		resp = e.get(t, "/api/v1/policies", token)
		assertStatus(t, resp, http.StatusOK)
		decodeData(t, resp, &list)
		if len(list.Items) != 1 {
			t.Fatalf("list returned %d policies, want 1", len(list.Items))
		}
		return single, list.Items[0]
	}

	t.Run("user role", func(t *testing.T) {
		single, listed := read(e.userToken(t))
		for name, v := range map[string]view{"GET /policies/{id}": single, "GET /policies": listed} {
			if v.HookPreBackup != "" || v.HookPostBackup != "" || v.HealthcheckURL != "" {
				t.Errorf("%s: hooks %q / %q and healthcheck %q, want all empty", name, v.HookPreBackup, v.HookPostBackup, v.HealthcheckURL)
			}
			for _, secret := range []string{"hunter2", "s3cret", "mysqldump", "pg_dump"} {
				if strings.Contains(v.Sources, secret) {
					t.Errorf("%s: sources %s contain %q", name, v.Sources, secret)
				}
			}
			for _, kept := range []string{`"/data"`, `"app-db"`, `"other-db"`} {
				if !strings.Contains(v.Sources, kept) {
					t.Errorf("%s: sources %s lost %s, want the sources themselves listed", name, v.Sources, kept)
				}
			}
		}
	})

	t.Run("admin", func(t *testing.T) {
		single, listed := read(e.adminToken(t))
		for name, v := range map[string]view{"GET /policies/{id}": single, "GET /policies": listed} {
			if v.HookPreBackup != hook || v.HealthcheckURL != pingURL || v.Sources != sourcesJSON {
				t.Errorf("%s: admin view = %+v, want the stored hooks, healthcheck URL and sources", name, v)
			}
		}
	})
}
