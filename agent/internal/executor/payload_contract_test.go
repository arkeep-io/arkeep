package executor

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// TestRestorePayloadContract decodes the golden restore payload the server
// test pins (shared/testdata/contract/restore.json). DisallowUnknownFields
// makes a field the server adds and the agent ignores fail here.
func TestRestorePayloadContract(t *testing.T) {
	raw, err := os.ReadFile("../../../shared/testdata/contract/restore.json")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var p restorePayload
	if err := dec.Decode(&p); err != nil {
		t.Fatalf("agent cannot decode the server restore payload: %v", err)
	}
	if p.ResticSnapshotID == "" || p.RepoPassword == "" || p.TargetPath == "" {
		t.Errorf("snapshot id, repo password or target path not decoded: %+v", p)
	}
	if len(p.IncludePaths) == 0 {
		t.Error("include_paths not decoded")
	}
	d := p.Destination
	if d.Type == "" || d.RepoURL == "" || len(d.Env) == 0 {
		t.Errorf("destination type, repo_url or env not decoded: %+v", d)
	}
}

// TestRepairPayloadContract decodes the golden repair payload the server test
// pins (shared/testdata/contract/repair.json), refusing fields the agent
// would ignore.
func TestRepairPayloadContract(t *testing.T) {
	raw, err := os.ReadFile("../../../shared/testdata/contract/repair.json")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var p repairPayload
	if err := dec.Decode(&p); err != nil {
		t.Fatalf("agent cannot decode the server repair payload: %v", err)
	}
	if p.RepoPassword == "" || p.Destination.RepoURL == "" || len(p.Destination.Env) == 0 {
		t.Errorf("password, repo_url or env not decoded: %+v", p)
	}
	r := p.Repair
	if r.Action == "" || !r.Apply || !r.Forget || len(r.Hosts) == 0 || len(r.Tags) == 0 || len(r.SnapshotIDs) == 0 {
		t.Errorf("repair parameters not decoded: %+v", r)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("the golden repair parameters do not validate: %v", err)
	}
}
