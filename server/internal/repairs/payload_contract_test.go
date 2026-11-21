package repairs

import (
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"testing"

	"github.com/arkeep-io/arkeep/shared/repair"
)

var updateContract = flag.Bool("update-contract", false, "rewrite the payload contract golden file")

// TestRepairPayloadContract pins the JSON the server sends to the agent for a
// repair (shared/testdata/contract/repair.json). The agent test decodes the
// same file, so a field rename on either side fails a test.
func TestRepairPayloadContract(t *testing.T) {
	const path = "../../../shared/testdata/contract/repair.json"
	payload := repairPayload{
		Destination: destinationPayload{
			DestinationID: "dest-1",
			Type:          "s3",
			RepoURL:       "s3:https://s3.example.com/bucket/repo",
			Credentials:   `{"access_key_id":"key"}`,
			Config:        `{"endpoint":"s3.example.com"}`,
			Env:           map[string]string{"AWS_ACCESS_KEY_ID": "key"},
		},
		RepoPassword: "repo-secret",
		Repair: repair.Params{
			Action:      repair.Snapshots,
			Apply:       true,
			Forget:      true,
			Hosts:       []string{"helios"},
			Tags:        []string{"policy:019f8ab0-ff71-7dc8-a8a9-4ec2237387e8"},
			SnapshotIDs: []string{"fea2c19e"},
		},
	}
	got, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if *updateContract {
		if err := os.WriteFile(path, append(got, '\n'), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update-contract): %v", err)
	}
	var gotV, wantV any
	if err := json.Unmarshal(got, &gotV); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantV); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotV, wantV) {
		t.Errorf("payload differs from repair.json (run with -update-contract if intended)\ngot:  %s\nwant: %s", got, want)
	}
}
