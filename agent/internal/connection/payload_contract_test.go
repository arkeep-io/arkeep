package connection

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// strictContractDecode decodes a golden payload with DisallowUnknownFields so
// a field the server adds and the agent ignores fails the test.
//
// destination.destination_id is dropped first: the server sends it but the
// connection manager's snapshotAccess deliberately does not read it (the agent
// only needs type, repo_url and env to open the repository).
func strictContractDecode(t *testing.T, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile("../../../shared/testdata/contract/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	dest, _ := m["destination"].(map[string]any)
	delete(dest, "destination_id")
	raw, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("agent cannot decode the server %s payload: %v", name, err)
	}
}

func checkAccess(t *testing.T, a snapshotAccess) {
	t.Helper()
	if a.ResticSnapshotID == "" || a.RepoPassword == "" {
		t.Errorf("snapshot id or repo password not decoded: %+v", a)
	}
	if a.Destination.Type == "" || a.Destination.RepoURL == "" || len(a.Destination.Env) == 0 {
		t.Errorf("destination type, repo_url or env not decoded: %+v", a.Destination)
	}
}

func TestBrowsePayloadContract(t *testing.T) {
	var p snapshotBrowsePayload
	strictContractDecode(t, "browse.json", &p)
	checkAccess(t, p.snapshotAccess)
	if p.Path == "" {
		t.Error("path not decoded")
	}
}

func TestDownloadPayloadContract(t *testing.T) {
	var p snapshotDownloadPayload
	strictContractDecode(t, "download.json", &p)
	checkAccess(t, p.snapshotAccess)
	if p.Path == "" || !p.Archive {
		t.Errorf("path or archive not decoded: %+v", p)
	}
}
