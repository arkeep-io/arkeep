// Package repair defines the repository repair actions (ADR 0026) and their
// parameters, shared by the server, which validates and records a request,
// and the agent, which validates it again and turns it into restic arguments.
package repair

import (
	"errors"
	"fmt"
	"regexp"
)

// Actions. Each runs one restic command on the destination's repository.
const (
	// Unlock runs `restic unlock`, which only removes stale locks.
	Unlock = "unlock"
	// Index runs `restic repair index`.
	Index = "repair-index"
	// Snapshots runs `restic repair snapshots`, as a dry run unless Apply is set.
	Snapshots = "repair-snapshots"
	// Packs runs `restic repair packs`.
	Packs = "repair-packs"
	// FindPacks runs `restic find --pack`: the read-only preview of Packs,
	// which restic offers no dry run for.
	FindPacks = "find-packs"
	// Check runs `restic check`, so that a check can run while the destination
	// is in maintenance and no other job can claim it.
	Check = "check"
)

// Actions lists the valid actions, in the order the GUI presents them.
var Actions = []string{Unlock, Index, Snapshots, Packs, FindPacks, Check}

// maxIDs caps the snapshot or pack IDs of one request: enough for a real
// repair, small enough to keep the argument list and the job record bounded.
const maxIDs = 100

// maxFilters caps the host and tag filters of one request.
const maxFilters = 20

var (
	// A snapshot ID, full or the short form restic prints (at least 8
	// characters).
	snapshotIDRe = regexp.MustCompile(`^[0-9a-f]{8,64}$`)
	// restic resolves pack IDs in full only.
	packIDRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// Host names as restic records them: no spaces, nothing a shell or
	// restic's flag parser would read differently.
	hostRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`)
	// Tags such as Arkeep's "policy:<id>" and "policy:<id>:command:<name>".
	// No comma: restic reads "a,b" in one --tag as "a and b".
	tagRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@+=-]{0,254}$`)
)

// Params is one repair request. The JSON form is the job payload's
// "repair" field and the parameters recorded on the job. It holds no
// secret.
type Params struct {
	Action string `json:"action"`

	// ReadAllPacks makes repair-index read every pack file instead of
	// trusting the existing index.
	ReadAllPacks bool `json:"read_all_packs,omitempty"`

	// Apply makes repair-snapshots write its changes. Without it the command
	// runs with --dry-run: a request that forgets the field changes nothing.
	Apply bool `json:"apply,omitempty"`
	// Forget makes repair-snapshots remove the original snapshots it repaired,
	// keeping only the repaired copies.
	Forget bool `json:"forget,omitempty"`
	// Hosts, Tags and SnapshotIDs restrict repair-snapshots to the matching
	// snapshots. Empty means every snapshot.
	Hosts       []string `json:"hosts,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	SnapshotIDs []string `json:"snapshot_ids,omitempty"`

	// PackIDs are the packs repair-packs salvages, or find-packs looks up.
	PackIDs []string `json:"pack_ids,omitempty"`

	// ReadData makes check read all pack data (restic check --read-data).
	ReadData bool `json:"read_data,omitempty"`
}

// Destructive reports whether the request changes the repository in a way
// that cannot be undone: repair-snapshots that writes and removes the
// originals, and repair-packs. Those need a preview and a confirmation.
func (p Params) Destructive() bool {
	switch p.Action {
	case Snapshots:
		return p.Apply && p.Forget
	case Packs:
		return true
	}
	return false
}

// Preview reports whether the request is the read-only preview of a
// destructive one: find-packs, or repair-snapshots as a dry run.
func (p Params) Preview() bool {
	return p.Action == FindPacks || (p.Action == Snapshots && !p.Apply)
}

// Validate checks the action and every parameter that comes from the user,
// and rejects parameters that do not apply to the action, so that a request
// never means something other than what it says.
func (p Params) Validate() error {
	switch p.Action {
	case Unlock, Index, Snapshots, Packs, FindPacks, Check:
	case "":
		return errors.New("repair action is required")
	default:
		return fmt.Errorf("unknown repair action %q", p.Action)
	}

	if p.ReadAllPacks && p.Action != Index {
		return errors.New("read_all_packs only applies to repair-index")
	}
	if p.Action != Snapshots {
		if p.Apply || p.Forget || len(p.Hosts) > 0 || len(p.Tags) > 0 || len(p.SnapshotIDs) > 0 {
			return errors.New("apply, forget, hosts, tags and snapshot_ids only apply to repair-snapshots")
		}
	}
	if p.Forget && !p.Apply {
		return errors.New("forget needs apply: a dry run removes nothing")
	}
	if p.ReadData && p.Action != Check {
		return errors.New("read_data only applies to check")
	}
	needsPacks := p.Action == Packs || p.Action == FindPacks
	if !needsPacks && len(p.PackIDs) > 0 {
		return errors.New("pack_ids only apply to repair-packs and find-packs")
	}
	if needsPacks && len(p.PackIDs) == 0 {
		return fmt.Errorf("%s needs at least one pack ID", p.Action)
	}

	if err := checkAll("pack ID", p.PackIDs, maxIDs, packIDRe, "64 lowercase hexadecimal characters"); err != nil {
		return err
	}
	if err := checkAll("snapshot ID", p.SnapshotIDs, maxIDs, snapshotIDRe, "8 to 64 lowercase hexadecimal characters"); err != nil {
		return err
	}
	if err := checkAll("host", p.Hosts, maxFilters, hostRe, "letters, digits, '.', '_' and '-', starting with a letter or digit"); err != nil {
		return err
	}
	return checkAll("tag", p.Tags, maxFilters, tagRe, "letters, digits and '.', '_', ':', '@', '+', '=', '-', starting with a letter or digit")
}

func checkAll(what string, values []string, limit int, re *regexp.Regexp, rule string) error {
	if len(values) > limit {
		return fmt.Errorf("at most %d %ss per request", limit, what)
	}
	for _, v := range values {
		if !re.MatchString(v) {
			return fmt.Errorf("invalid %s %q: expected %s", what, v, rule)
		}
	}
	return nil
}
