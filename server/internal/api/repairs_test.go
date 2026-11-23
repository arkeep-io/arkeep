package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/shared/proto"
	"github.com/arkeep-io/arkeep/shared/repair"
)

const repairPackID = "0606cc8ce3912abf19fecc7546fb74a5ce00651b833fb00a35a8db471aad1bc0"

// repairableDestination creates a destination whose maintenance agent is
// connected (a fake stream) and reports the repair capability, and whose
// repository password is known.
func repairableDestination(t *testing.T, e *testEnv) (*db.Destination, *fakeAgentStream) {
	t.Helper()
	ctx := context.Background()
	stream := &fakeAgentStream{}
	agentID := uuid.MustParse(connectDBFakeAgent(t, e, stream))
	agent, err := e.deps.agents.GetByID(ctx, agentID)
	if err != nil {
		t.Fatal(err)
	}
	agent.RepairAvailable = true
	if err := e.deps.agents.Update(ctx, agent); err != nil {
		t.Fatal(err)
	}
	dest := createDBDestination(t, e.deps, "garage", "s3")
	dest.RetentionAgentID = &agentID
	dest.RepoPassword = db.EncryptedString("repo-password")
	if err := e.deps.dests.Update(ctx, dest); err != nil {
		t.Fatal(err)
	}
	return dest, stream
}

func repairAssignments(stream *fakeAgentStream) []*proto.JobAssignment {
	var out []*proto.JobAssignment
	for _, a := range stream.assignments() {
		if a.Type == proto.JobType_JOB_TYPE_REPAIR {
			out = append(out, a)
		}
	}
	return out
}

// TestRepair_DestructiveNeedsTheDestinationName: repair-snapshots --forget
// and repair-packs change the repository for good. They are refused unless
// the request carries the destination's name, which the GUI asks the admin
// to type.
func TestRepair_DestructiveNeedsTheDestinationName(t *testing.T) {
	e := newTestEnv(t)
	dest, stream := repairableDestination(t, e)
	path := "/api/v1/destinations/" + dest.ID.String() + "/repair"

	for _, body := range []map[string]any{
		{"action": repair.Snapshots, "apply": true, "forget": true},
		{"action": repair.Packs, "pack_ids": []string{repairPackID}},
		{"action": repair.Packs, "pack_ids": []string{repairPackID}, "confirm": "Garage"},
	} {
		resp := e.post(t, path, e.adminToken(t), body)
		assertStatus(t, resp, http.StatusBadRequest)
	}
	if n := len(repairAssignments(stream)); n != 0 {
		t.Fatalf("%d repairs dispatched without the destination's name", n)
	}

	resp := e.post(t, path, e.adminToken(t), map[string]any{"action": repair.Packs, "pack_ids": []string{repairPackID}, "confirm": "garage"})
	assertStatus(t, resp, http.StatusOK)
	var out repairResponse
	decodeData(t, resp, &out)
	if !out.MaintenanceStarted {
		t.Error("the repair did not report the maintenance it started")
	}
	if sent := repairAssignments(stream); len(sent) != 1 || sent[0].JobId != out.JobID {
		t.Fatalf("dispatched = %v, want the confirmed repair", sent)
	}
}

// TestRepair_RecordedOnTheJob: the job shows who started the repair and with
// which parameters, and the destination shows the maintenance it started.
func TestRepair_RecordedOnTheJob(t *testing.T) {
	e := newTestEnv(t)
	dest, _ := repairableDestination(t, e)

	resp := e.post(t, "/api/v1/destinations/"+dest.ID.String()+"/repair", e.adminToken(t),
		map[string]any{"action": repair.Snapshots, "hosts": []string{"helios"}})
	assertStatus(t, resp, http.StatusOK)
	var out repairResponse
	decodeData(t, resp, &out)

	resp = e.get(t, "/api/v1/jobs/"+out.JobID, e.adminToken(t))
	assertStatus(t, resp, http.StatusOK)
	var job jobResponse
	decodeData(t, resp, &job)
	var params repair.Params
	if err := json.Unmarshal(job.Params, &params); err != nil {
		t.Fatalf("job params %s: %v", job.Params, err)
	}
	if job.Type != "repair" || job.RequestedBy == "" || params.Action != repair.Snapshots || len(params.Hosts) != 1 || params.Apply {
		t.Errorf("job = %+v (params %+v), want the dry-run repair and its requester", job, params)
	}

	resp = e.get(t, "/api/v1/destinations/"+dest.ID.String(), e.adminToken(t))
	assertStatus(t, resp, http.StatusOK)
	var d destinationResponse
	decodeData(t, resp, &d)
	if !d.RepairAvailable {
		t.Error("repair_available = false for an agent that reported the capability")
	}
	if d.Maintenance == nil || d.Maintenance.RepairJobID != out.JobID || !d.Maintenance.Effective {
		t.Errorf("maintenance = %+v, want the repair's effective maintenance", d.Maintenance)
	}
}

// TestMaintenance_HoldsJobsUntilItEnds: an admin's maintenance stops jobs
// from claiming the destination, a repair cannot end it, and ending it lets
// the destination be claimed again.
func TestMaintenance_HoldsJobsUntilItEnds(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	dest, _ := repairableDestination(t, e)
	path := "/api/v1/destinations/" + dest.ID.String() + "/maintenance"

	resp := e.doJSON(t, http.MethodPut, path, e.adminToken(t), map[string]any{"enabled": true})
	assertStatus(t, resp, http.StatusOK)

	backup := &db.Job{AgentID: *dest.RetentionAgentID, Type: "backup", Status: "pending"}
	if err := e.deps.jobs.Create(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.deps.dests.TryAcquireBusyAll(ctx, []uuid.UUID{dest.ID}, backup.ID, db.LockShared); err != nil || ok {
		t.Fatalf("a backup claimed a destination in maintenance: %v %v", ok, err)
	}

	resp = e.post(t, "/api/v1/destinations/"+dest.ID.String()+"/repair", e.adminToken(t), map[string]any{"action": repair.Index})
	assertStatus(t, resp, http.StatusOK)
	var out repairResponse
	decodeData(t, resp, &out)
	if out.MaintenanceStarted {
		t.Error("the repair reported starting a maintenance the admin had started")
	}

	resp = e.doJSON(t, http.MethodPut, path, e.adminToken(t), map[string]any{"enabled": false})
	_ = resp.Body.Close()
	assertStatus(t, resp, http.StatusConflict)

	if err := e.deps.jobs.UpdateStatus(ctx, uuid.MustParse(out.JobID), "succeeded", nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	resp = e.doJSON(t, http.MethodPut, path, e.adminToken(t), map[string]any{"enabled": false})
	assertStatus(t, resp, http.StatusOK)
	if ok, err := e.deps.dests.TryAcquireBusyAll(ctx, []uuid.UUID{dest.ID}, backup.ID, db.LockShared); err != nil || !ok {
		t.Errorf("the backup could not claim the destination after the maintenance: %v %v", ok, err)
	}
}

func TestRepair_RefusedWhenBusyOrUnsupported(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	dest, stream := repairableDestination(t, e)
	path := "/api/v1/destinations/" + dest.ID.String() + "/repair"

	backup := &db.Job{AgentID: *dest.RetentionAgentID, Type: "backup", Status: "running"}
	if err := e.deps.jobs.Create(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.deps.dests.TryAcquireBusyAll(ctx, []uuid.UUID{dest.ID}, backup.ID, db.LockShared); err != nil || !ok {
		t.Fatalf("hold the destination: %v %v", ok, err)
	}
	resp := e.post(t, path, e.adminToken(t), map[string]any{"action": repair.Unlock})
	_ = resp.Body.Close()
	assertStatus(t, resp, http.StatusConflict)

	if err := e.deps.jobs.UpdateStatus(ctx, backup.ID, "succeeded", nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	agent, err := e.deps.agents.GetByID(ctx, *dest.RetentionAgentID)
	if err != nil {
		t.Fatal(err)
	}
	agent.RepairAvailable = false
	if err := e.deps.agents.Update(ctx, agent); err != nil {
		t.Fatal(err)
	}
	resp = e.post(t, path, e.adminToken(t), map[string]any{"action": repair.Unlock})
	_ = resp.Body.Close()
	assertStatus(t, resp, http.StatusConflict)

	resp = e.post(t, path, e.adminToken(t), map[string]any{"action": "prune"})
	_ = resp.Body.Close()
	assertStatus(t, resp, http.StatusBadRequest)

	if n := len(repairAssignments(stream)); n != 0 {
		t.Errorf("%d repairs dispatched although every request was refused", n)
	}
}
