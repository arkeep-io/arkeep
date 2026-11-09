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
