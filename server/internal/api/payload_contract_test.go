package api

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The golden files in shared/testdata/contract are the JSON the server sends
// to the agent for restore, snapshot browse and snapshot download. The agent
// tests decode the same files, so a field rename on either side fails a test.
var updateContract = flag.Bool("update-contract", false, "rewrite the payload contract golden files")

const contractDir = "../../../shared/testdata/contract/"

func contractDestination() destinationFields {
	return destinationFields{
		DestinationID: "dest-1",
		Type:          "s3",
		RepoURL:       "s3:https://s3.example.com/bucket/repo",
		Env:           map[string]string{"AWS_ACCESS_KEY_ID": "key"},
	}
}

func contractAccess() snapshotRepoAccess {
	return snapshotRepoAccess{
		ResticSnapshotID: "abc123def456",
		RepoPassword:     "repo-secret",
		Destination:      contractDestination(),
	}
}

func TestPayloadContract(t *testing.T) {
	cases := map[string]any{
		"restore.json": restorePayload{
			ResticSnapshotID: "abc123def456",
			RepoPassword:     "repo-secret",
			TargetPath:       "/restore/target",
			IncludePaths:     []string{"/data/docs"},
			Destination:      contractDestination(),
		},
		"browse.json": snapshotBrowsePayload{
			snapshotRepoAccess: contractAccess(),
			Path:               "/data",
		},
		"download.json": snapshotDownloadPayload{
			snapshotRepoAccess: contractAccess(),
			Path:               "/data/docs",
			Archive:            true,
		},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := json.MarshalIndent(payload, "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			path := filepath.Join(contractDir, name)
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
				t.Errorf("payload differs from %s (run with -update-contract if intended)\ngot:  %s\nwant: %s", name, got, want)
			}
		})
	}
}
