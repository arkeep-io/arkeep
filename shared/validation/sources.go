// Package validation holds validation rules shared between the server and
// the agent, so a single rule is enforced identically wherever it matters
// rather than risking drift between two copies.
package validation

import (
	"errors"
	"fmt"
	"strings"
)

const sourceMaxLen = 4096

// ValidateSourceEntry validates a single flattened backup source path before
// it is persisted (server) and again before it is handed to restic (agent).
//
// The agent appends backup sources directly to restic's argv after a "--"
// end-of-options marker, which stops restic from parsing them as flags. This
// check is defense in depth for that same threat: it rejects entries that
// look like flags (e.g. "--password-command=...") so a malformed or
// already-stored policy can never reach restic with an argument-like source,
// even if the "--" marker were ever removed or bypassed by a code path that
// doesn't use it.
//
// The raw (untrimmed) string is checked: a leading space means restic's flag
// parser would not treat the entry as a flag either, so trimming first would
// only weaken the check.
func ValidateSourceEntry(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("source path must not be empty")
	}
	if len(path) > sourceMaxLen {
		return fmt.Errorf("source path must not exceed %d characters", sourceMaxLen)
	}
	if strings.HasPrefix(path, "-") {
		return fmt.Errorf("source path %q must not start with \"-\": restic would interpret it as a command-line flag", path)
	}
	return nil
}
