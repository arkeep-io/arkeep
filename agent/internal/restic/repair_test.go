package restic

import (
	"slices"
	"strings"
	"testing"

	"github.com/arkeep-io/arkeep/shared/repair"
)

const testPackID = "0606cc8ce3912abf19fecc7546fb74a5ce00651b833fb00a35a8db471aad1bc0"

func TestBuildRepairArgs(t *testing.T) {
	tests := []struct {
		name   string
		params repair.Params
		want   []string
	}{
		{"unlock", repair.Params{Action: repair.Unlock}, []string{"unlock"}},
		{"repair index", repair.Params{Action: repair.Index}, []string{"repair", "index"}},
		{"repair index reading all packs", repair.Params{Action: repair.Index, ReadAllPacks: true}, []string{"repair", "index", "--read-all-packs"}},
		{"repair snapshots is a dry run by default", repair.Params{Action: repair.Snapshots}, []string{"repair", "snapshots", "--dry-run", "--"}},
		{
			"repair snapshots applied with filters",
			repair.Params{Action: repair.Snapshots, Apply: true, Forget: true, Hosts: []string{"helios", "selene"}, Tags: []string{"policy:abc"}, SnapshotIDs: []string{"fea2c19e"}},
			[]string{"repair", "snapshots", "--forget", "--host", "helios", "--host", "selene", "--tag", "policy:abc", "--", "fea2c19e"},
		},
		{"repair packs", repair.Params{Action: repair.Packs, PackIDs: []string{testPackID}}, []string{"repair", "packs", "--", testPackID}},
		{"find packs", repair.Params{Action: repair.FindPacks, PackIDs: []string{testPackID}}, []string{"find", "--pack", "--", testPackID}},
		{"structural check", repair.Params{Action: repair.Check}, []string{"check"}},
		{"full check", repair.Params{Action: repair.Check, ReadData: true}, []string{"check", "--read-data"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildRepairArgs(tt.params)
			if err != nil {
				t.Fatalf("BuildRepairArgs() error: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("BuildRepairArgs() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBuildRepairArgs_RefusesInvalidParams: the agent validates again what
// the server already validated, so a payload that bypassed the server never
// reaches restic.
func TestBuildRepairArgs_RefusesInvalidParams(t *testing.T) {
	for _, p := range []repair.Params{
		{Action: "prune"},
		{Action: repair.Packs},
		{Action: repair.FindPacks, PackIDs: []string{"--password-command=sh"}},
		{Action: repair.Snapshots, Hosts: []string{"-h"}},
		{Action: repair.Snapshots, Forget: true},
	} {
		if args, err := BuildRepairArgs(p); err == nil {
			t.Errorf("BuildRepairArgs(%+v) = %q, want an error", p, args)
		}
	}
}

func TestSupportsRepair(t *testing.T) {
	for v, want := range map[string]bool{
		"0.19.1":  true,
		"0.16.0":  true,
		"0.16.4":  true,
		"1.0.0":   true,
		"0.15.2":  false,
		"0.9.6":   false,
		"":        false,
		"garbage": false,
	} {
		if got := SupportsRepair(v); got != want {
			t.Errorf("SupportsRepair(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestVersionRe(t *testing.T) {
	m := versionRe.FindStringSubmatch("restic 0.19.1 compiled with go1.26.4 on linux/amd64")
	if m == nil || strings.Join(m[1:], ".") != "0.19.1" {
		t.Errorf("versionRe match = %q, want 0.19.1", m)
	}
	if versionRe.MatchString("rclone v1.76.0") {
		t.Error("versionRe matched rclone's version line")
	}
}
