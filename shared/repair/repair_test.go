package repair

import (
	"strings"
	"testing"
)

const (
	packA = "0606cc8ce3912abf19fecc7546fb74a5ce00651b833fb00a35a8db471aad1bc0"
	snapA = "fea2c19e"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		params  Params
		wantErr string
	}{
		{name: "unlock", params: Params{Action: Unlock}},
		{name: "repair index", params: Params{Action: Index, ReadAllPacks: true}},
		{name: "repair snapshots dry run", params: Params{Action: Snapshots, Hosts: []string{"homeassistant"}, Tags: []string{"policy:019f8ab0-ff71-7dc8-a8a9-4ec2237387e8:command:pg_dump"}, SnapshotIDs: []string{snapA}}},
		{name: "repair snapshots forget", params: Params{Action: Snapshots, Apply: true, Forget: true}},
		{name: "repair packs", params: Params{Action: Packs, PackIDs: []string{packA}}},
		{name: "find packs", params: Params{Action: FindPacks, PackIDs: []string{packA}}},
		{name: "full check", params: Params{Action: Check, ReadData: true}},

		{name: "no action", params: Params{}, wantErr: "action is required"},
		{name: "unknown action", params: Params{Action: "prune"}, wantErr: `unknown repair action "prune"`},
		{name: "forget without apply", params: Params{Action: Snapshots, Forget: true}, wantErr: "forget needs apply"},
		{name: "filters on another action", params: Params{Action: Index, Hosts: []string{"a"}}, wantErr: "only apply to repair-snapshots"},
		{name: "read_all_packs on another action", params: Params{Action: Check, ReadAllPacks: true}, wantErr: "only applies to repair-index"},
		{name: "read_data on another action", params: Params{Action: Index, ReadData: true}, wantErr: "only applies to check"},
		{name: "packs without IDs", params: Params{Action: Packs}, wantErr: "needs at least one pack ID"},
		{name: "pack IDs on another action", params: Params{Action: Unlock, PackIDs: []string{packA}}, wantErr: "only apply to repair-packs"},
		{name: "short pack ID", params: Params{Action: Packs, PackIDs: []string{"0606cc8c"}}, wantErr: "invalid pack ID"},
		{name: "pack ID as a flag", params: Params{Action: FindPacks, PackIDs: []string{"--password-command=sh"}}, wantErr: "invalid pack ID"},
		{name: "upper-case snapshot ID", params: Params{Action: Snapshots, SnapshotIDs: []string{"FEA2C19E"}}, wantErr: "invalid snapshot ID"},
		{name: "snapshot ID too short", params: Params{Action: Snapshots, SnapshotIDs: []string{"fea2c1"}}, wantErr: "invalid snapshot ID"},
		{name: "host as a flag", params: Params{Action: Snapshots, Hosts: []string{"-h"}}, wantErr: "invalid host"},
		{name: "host with a space", params: Params{Action: Snapshots, Hosts: []string{"my host"}}, wantErr: "invalid host"},
		{name: "tag with a comma", params: Params{Action: Snapshots, Tags: []string{"a,b"}}, wantErr: "invalid tag"},
		{name: "tag with a shell character", params: Params{Action: Snapshots, Tags: []string{"a;rm"}}, wantErr: "invalid tag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("Validate() = %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidate_Limits(t *testing.T) {
	ids := make([]string, maxIDs+1)
	for i := range ids {
		ids[i] = packA
	}
	if err := (Params{Action: Packs, PackIDs: ids}).Validate(); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("Validate() with %d pack IDs = %v, want the limit error", len(ids), err)
	}
	hosts := make([]string, maxFilters+1)
	for i := range hosts {
		hosts[i] = "h"
	}
	if err := (Params{Action: Snapshots, Hosts: hosts}).Validate(); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("Validate() with %d hosts = %v, want the limit error", len(hosts), err)
	}
}

func TestDestructive(t *testing.T) {
	tests := []struct {
		params Params
		want   bool
	}{
		{Params{Action: Unlock}, false},
		{Params{Action: Index}, false},
		{Params{Action: Snapshots}, false},
		{Params{Action: Snapshots, Apply: true}, false},
		{Params{Action: Snapshots, Apply: true, Forget: true}, true},
		{Params{Action: Packs, PackIDs: []string{packA}}, true},
		{Params{Action: FindPacks, PackIDs: []string{packA}}, false},
		{Params{Action: Check, ReadData: true}, false},
	}
	for _, tt := range tests {
		if got := tt.params.Destructive(); got != tt.want {
			t.Errorf("%+v.Destructive() = %v, want %v", tt.params, got, tt.want)
		}
	}
}

func TestPreview(t *testing.T) {
	tests := []struct {
		params Params
		want   bool
	}{
		{Params{Action: Unlock}, false},
		{Params{Action: Index}, false},
		{Params{Action: Snapshots}, true},
		{Params{Action: Snapshots, Apply: true}, false},
		{Params{Action: Snapshots, Apply: true, Forget: true}, false},
		{Params{Action: Packs, PackIDs: []string{packA}}, false},
		{Params{Action: FindPacks, PackIDs: []string{packA}}, true},
		{Params{Action: Check}, false},
	}
	for _, tt := range tests {
		if got := tt.params.Preview(); got != tt.want {
			t.Errorf("%+v.Preview() = %v, want %v", tt.params, got, tt.want)
		}
	}
}
