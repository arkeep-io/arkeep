package api

import (
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/arkeep-io/arkeep/server/internal/policyutil"
	"github.com/arkeep-io/arkeep/shared/validation"
)

// commandSourceNameRe constrains a command source's name. The name is
// interpolated into a restic retention tag (policy:<id>:command:<name>) and
// used as the --stdin-filename, so it must not contain ":" (which could
// forge another source's tag and get its snapshots pruned), a path
// separator, whitespace, or a leading "-" or "." (argv and filename safety).
var commandSourceNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// validateSourcesJSON validates a policy's sources field at save time. It
// flattens the sources JSON the same way the scheduler does at dispatch time
// (via policyutil.SourcePaths/CommandSources), so validation checks exactly
// what will later reach the agent.
func validateSourcesJSON(sources string) error {
	paths, err := policyutil.SourcePaths(sources)
	if err != nil {
		return errors.New("sources must be a JSON array of paths or {type, path} objects")
	}
	cmds, err := policyutil.CommandSources(sources)
	if err != nil {
		return errors.New("sources must be a JSON array of paths or {type, path} objects")
	}
	if len(paths)+len(cmds) == 0 {
		return errors.New("at least one source is required")
	}
	for _, path := range paths {
		if err := validation.ValidateSourceEntry(path); err != nil {
			return err
		}
	}
	seenNames := make(map[string]bool, len(cmds))
	for _, c := range cmds {
		if !commandSourceNameRe.MatchString(c.Name) {
			return fmt.Errorf("command source name %q must be 1-64 characters of letters, digits, \".\", \"_\" or \"-\", starting with a letter or digit", c.Name)
		}
		if seenNames[c.Name] {
			return fmt.Errorf("duplicate command source name %q", c.Name)
		}
		seenNames[c.Name] = true
		if c.Command == "" {
			return fmt.Errorf("command source %q: command must not be empty", c.Name)
		}
		if err := validateHookCommand(c.Command); err != nil {
			return fmt.Errorf("command source %q: %w", c.Name, err)
		}
	}
	return nil
}

// policyHasCommandSource reports whether the sources JSON declares at least
// one command-type source. Malformed JSON returns false; validateSourcesJSON
// has already rejected it by the time the admin gate is consulted.
func policyHasCommandSource(sourcesJSON string) bool {
	cmds, err := policyutil.CommandSources(sourcesJSON)
	if err != nil {
		return false
	}
	return len(cmds) > 0
}

// commandSourcesChanged reports whether the command-type entries differ
// between two sources documents, ignoring directory/docker-volume entries
// and ordering. Fails closed: an unparsable document counts as changed.
func commandSourcesChanged(oldJSON, newJSON string) bool {
	oldCmds, err := policyutil.CommandSources(oldJSON)
	if err != nil {
		return true
	}
	newCmds, err := policyutil.CommandSources(newJSON)
	if err != nil {
		return true
	}
	if len(oldCmds) != len(newCmds) {
		return true
	}
	sortCommandSources(oldCmds)
	sortCommandSources(newCmds)
	for i := range oldCmds {
		if oldCmds[i] != newCmds[i] {
			return true
		}
	}
	return false
}

func sortCommandSources(cmds []policyutil.CommandSource) {
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
}
