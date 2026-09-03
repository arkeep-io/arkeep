package api

import (
	"errors"

	"github.com/arkeep-io/arkeep/server/internal/policyutil"
	"github.com/arkeep-io/arkeep/shared/validation"
)

// validateSourcesJSON validates a policy's sources field at save time. It
// flattens the sources JSON the same way the scheduler does at dispatch time
// (via policyutil.SourcePaths), so validation checks exactly the strings that
// will later be appended to restic's argv on the agent.
func validateSourcesJSON(sources string) error {
	paths, err := policyutil.SourcePaths(sources)
	if err != nil {
		return errors.New("sources must be a JSON array of paths or {type, path} objects")
	}
	if len(paths) == 0 {
		return errors.New("at least one source is required")
	}
	for _, path := range paths {
		if err := validation.ValidateSourceEntry(path); err != nil {
			return err
		}
	}
	return nil
}
