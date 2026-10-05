package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// createDBSnapshotOnDeletedDest inserts a policy snapshot on a destination of
// destType and then deletes the destination through the repository, the same
// path the API takes (soft delete + secret wipe). When deletePolicy is set the
// snapshot's policy is soft-deleted too, leaving no stored repository password.
func createDBSnapshotOnDeletedDest(t *testing.T, deps *testDeps, destType string, deletePolicy bool) *db.Snapshot {
	t.Helper()
	ctx := context.Background()
	job := createDBJob(t, deps)
	dest := createDBDestination(t, deps, "deleted-dest-"+uuid.NewString(), destType)
	dest.Config = `{"bucket":"b","endpoint":"s3.example.com"}`
	dest.RepoPassword = "dest-repo-secret"
	if err := deps.dests.Update(ctx, dest); err != nil {
		t.Fatalf("createDBSnapshotOnDeletedDest: update dest: %v", err)
	}
	s := &db.Snapshot{
		PolicyID:      job.PolicyID,
		DestinationID: dest.ID,
		JobID:         &job.ID,
		SnapshotID:    uuid.NewString(),
		SnapshotAt:    time.Now(),
	}
	if err := deps.snaps.Create(ctx, s); err != nil {
		t.Fatalf("createDBSnapshotOnDeletedDest: create snapshot: %v", err)
	}
	if err := deps.dests.Delete(ctx, dest.ID); err != nil {
		t.Fatalf("createDBSnapshotOnDeletedDest: delete dest: %v", err)
	}
	if deletePolicy {
		if err := deps.policies.Delete(ctx, *job.PolicyID); err != nil {
			t.Fatalf("createDBSnapshotOnDeletedDest: delete policy: %v", err)
		}
	}
	return s
}

// connectDBFakeAgent registers an online fake agent backed by a real agents
// row, so the restore job it receives satisfies the jobs.agent_id foreign key.
func connectDBFakeAgent(t *testing.T, e *testEnv, stream *fakeAgentStream) string {
	t.Helper()
	agent := createDBAgent(t, e.deps, "restore-agent-"+uuid.NewString())
	stream.mgr = e.mgr
	stream.agentID = agent.ID.String()
	e.mgr.Register(stream.agentID, "fake-host", false, stream)
	return stream.agentID
}

// restorePayloadSent decodes the single restore assignment the fake agent got.
func restorePayloadSent(t *testing.T, stream *fakeAgentStream) restorePayload {
	t.Helper()
	var restores []*proto.JobAssignment
	for _, a := range stream.assignments() {
		if a.Type == proto.JobType_JOB_TYPE_RESTORE {
			restores = append(restores, a)
		}
	}
	if len(restores) != 1 {
		t.Fatalf("agent received %d restore assignments, want 1", len(restores))
	}
	var p restorePayload
	if err := json.Unmarshal(restores[0].Payload, &p); err != nil {
		t.Fatalf("decode restore payload: %v", err)
	}
	return p
}

// assertBodyContains guards against a status code matching for an unrelated reason.
func assertBodyContains(t *testing.T, resp *http.Response, want string) {
	t.Helper()
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), want) {
		t.Errorf("body = %s, want it to contain %q", strings.TrimSpace(string(body)), want)
	}
}

func TestDestinationDelete_WipesSecrets(t *testing.T) {
	e := newTestEnv(t)
	dest := createDBDestination(t, e.deps, "to-delete", "s3")
	dest.RepoPassword = "repo-secret"
	if err := e.deps.dests.Update(context.Background(), dest); err != nil {
		t.Fatalf("update dest: %v", err)
	}

	resp := e.del(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t))
	assertStatus(t, resp, http.StatusNoContent)

	var raw struct {
		Credentials  string
		RepoPassword string
		DeletedAt    *time.Time
	}
	if err := e.deps.gdb.Raw(`SELECT credentials, repo_password, deleted_at FROM destinations WHERE id = ?`, dest.ID).
		Scan(&raw).Error; err != nil {
		t.Fatalf("read destination row: %v", err)
	}
	if raw.DeletedAt == nil {
		t.Error("deleted_at is NULL, want the row soft-deleted")
	}
	if raw.Credentials != "" {
		t.Errorf("credentials = %q, want wiped", raw.Credentials)
	}
	if raw.RepoPassword != "" {
		t.Errorf("repo_password = %q, want wiped", raw.RepoPassword)
	}

	// A second delete of the same destination is a 404, not a re-wipe.
	resp = e.del(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t))
	assertStatus(t, resp, http.StatusNotFound)
}

func TestSnapshotHandler_ListDeletedDestinationFlags(t *testing.T) {
	e := newTestEnv(t)
	live := createDBSnapshot(t, e.deps)
	policyAlive := createDBSnapshotOnDeletedDest(t, e.deps, "s3", false)
	policyGone := createDBSnapshotOnDeletedDest(t, e.deps, "s3", true)

	resp := e.get(t, "/api/v1/snapshots", e.adminToken(t))
	assertStatus(t, resp, http.StatusOK)
	var data struct {
		Items []snapshotResponse `json:"items"`
	}
	decodeData(t, resp, &data)

	got := map[string]snapshotResponse{}
	for _, it := range data.Items {
		got[it.ID] = it
	}
	cases := []struct {
		name            string
		id              uuid.UUID
		wantDeleted     bool
		wantPwdRequired bool
		wantType        string
	}{
		{"live destination", live.ID, false, false, "local"},
		{"deleted destination, live policy", policyAlive.ID, true, false, "s3"},
		{"deleted destination, deleted policy", policyGone.ID, true, true, "s3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			it, ok := got[tc.id.String()]
			if !ok {
				t.Fatalf("snapshot %s missing from list", tc.id)
			}
			if it.DestinationDeleted != tc.wantDeleted {
				t.Errorf("destination_deleted = %v, want %v", it.DestinationDeleted, tc.wantDeleted)
			}
			if it.RepoPasswordRequired != tc.wantPwdRequired {
				t.Errorf("repo_password_required = %v, want %v", it.RepoPasswordRequired, tc.wantPwdRequired)
			}
			if it.DestinationType != tc.wantType {
				t.Errorf("destination_type = %q, want %q", it.DestinationType, tc.wantType)
			}
		})
	}
}

func TestSnapshotHandler_RestoreDeletedDestination(t *testing.T) {
	s3Creds := map[string]string{"access_key": "AKIA-new", "secret_key": "new-secret"}

	t.Run("returns 422 without credentials", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshotOnDeletedDest(t, e.deps, "s3", false)
		agentID := connectFakeAgent(e, &fakeAgentStream{})

		resp := e.post(t, "/api/v1/snapshots/"+s.ID.String()+"/restore", e.adminToken(t),
			map[string]any{"agent_id": agentID, "target_path": "/restore"})
		assertStatus(t, resp, http.StatusUnprocessableEntity)
		assertBodyContains(t, resp, "access key")
	})

	t.Run("returns 422 for a partial restore", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshotOnDeletedDest(t, e.deps, "s3", false)
		agentID := connectFakeAgent(e, &fakeAgentStream{})

		resp := e.post(t, "/api/v1/snapshots/"+s.ID.String()+"/restore", e.adminToken(t),
			map[string]any{"agent_id": agentID, "target_path": "/restore", "include_paths": []string{"/a"}, "credentials": s3Creds})
		assertStatus(t, resp, http.StatusUnprocessableEntity)
		assertBodyContains(t, resp, "partial restore")
	})

	t.Run("returns 503 when the agent is offline instead of queueing", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshotOnDeletedDest(t, e.deps, "s3", false)
		agent := createDBAgent(t, e.deps, "offline-agent")

		resp := e.post(t, "/api/v1/snapshots/"+s.ID.String()+"/restore", e.adminToken(t),
			map[string]any{"agent_id": agent.ID.String(), "target_path": "/restore", "credentials": s3Creds})
		assertStatus(t, resp, http.StatusServiceUnavailable)

		var count int64
		e.deps.gdb.Model(&db.Job{}).Where("agent_id = ? AND type = ?", agent.ID, "restore").Count(&count)
		if count != 0 {
			t.Errorf("stored %d restore jobs, want 0 (supplied credentials cannot be queued)", count)
		}
	})

	t.Run("dispatches with supplied credentials and the policy password", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshotOnDeletedDest(t, e.deps, "s3", false)
		stream := &fakeAgentStream{}
		agentID := connectDBFakeAgent(t, e, stream)

		resp := e.post(t, "/api/v1/snapshots/"+s.ID.String()+"/restore", e.adminToken(t),
			map[string]any{"agent_id": agentID, "target_path": "/restore", "credentials": s3Creds, "repo_password": "ignored"})
		assertStatus(t, resp, http.StatusOK)

		p := restorePayloadSent(t, stream)
		if p.Destination.Env["AWS_ACCESS_KEY_ID"] != "AKIA-new" || p.Destination.Env["AWS_SECRET_ACCESS_KEY"] != "new-secret" {
			t.Errorf("env = %v, want the supplied S3 credentials", p.Destination.Env)
		}
		if p.Destination.RepoURL == "" {
			t.Error("repo_url is empty, want it rebuilt from the retained config")
		}
		if p.RepoPassword != "secret" {
			t.Errorf("repo password = %q, want the live policy's password", p.RepoPassword)
		}

		// The supplied credentials must not have been written back.
		var stored string
		e.deps.gdb.Raw(`SELECT credentials FROM destinations WHERE id = ?`, s.DestinationID).Scan(&stored)
		if stored != "" {
			t.Errorf("stored credentials = %q, want still wiped", stored)
		}
	})

	t.Run("returns 422 without repo password when the policy is gone", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshotOnDeletedDest(t, e.deps, "s3", true)
		agentID := connectFakeAgent(e, &fakeAgentStream{})

		resp := e.post(t, "/api/v1/snapshots/"+s.ID.String()+"/restore", e.adminToken(t),
			map[string]any{"agent_id": agentID, "target_path": "/restore", "credentials": s3Creds})
		assertStatus(t, resp, http.StatusUnprocessableEntity)
		assertBodyContains(t, resp, "repository password")
	})

	t.Run("uses the supplied repo password when the policy is gone", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshotOnDeletedDest(t, e.deps, "s3", true)
		stream := &fakeAgentStream{}
		agentID := connectDBFakeAgent(t, e, stream)

		resp := e.post(t, "/api/v1/snapshots/"+s.ID.String()+"/restore", e.adminToken(t),
			map[string]any{"agent_id": agentID, "target_path": "/restore", "credentials": s3Creds, "repo_password": "typed-secret"})
		assertStatus(t, resp, http.StatusOK)

		if p := restorePayloadSent(t, stream); p.RepoPassword != "typed-secret" {
			t.Errorf("repo password = %q, want the supplied one", p.RepoPassword)
		}
	})

	t.Run("local destination needs no credentials", func(t *testing.T) {
		e := newTestEnv(t)
		s := createDBSnapshotOnDeletedDest(t, e.deps, "local", false)
		stream := &fakeAgentStream{}
		agentID := connectDBFakeAgent(t, e, stream)

		resp := e.post(t, "/api/v1/snapshots/"+s.ID.String()+"/restore", e.adminToken(t),
			map[string]any{"agent_id": agentID, "target_path": "/restore"})
		assertStatus(t, resp, http.StatusOK)
		restorePayloadSent(t, stream)
	})
}

func TestSnapshotHandler_BrowseDeletedDestination(t *testing.T) {
	e := newTestEnv(t)
	s := createDBSnapshotOnDeletedDest(t, e.deps, "s3", false)
	agentID := connectFakeAgent(e, &fakeAgentStream{})

	resp := e.get(t, "/api/v1/snapshots/"+s.ID.String()+"/browse?agent_id="+agentID, e.adminToken(t))
	assertStatus(t, resp, http.StatusUnprocessableEntity)
	assertBodyContains(t, resp, "was deleted")

	resp = e.post(t, "/api/v1/snapshots/"+s.ID.String()+"/download", e.adminToken(t),
		map[string]any{"path": "/a", "type": "file", "agent_id": agentID})
	assertStatus(t, resp, http.StatusUnprocessableEntity)
}
