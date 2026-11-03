// Package policyutil provides helpers for interpreting backup policy fields.
// SourcePaths and CommandSources are shared between the scheduler (backup
// dispatch) and the API layer (policy validation) so both operate on the
// exact same flattened source list — the API validates precisely what the
// scheduler will later send to the agent.
package policyutil

import (
	"encoding/json"
	"fmt"
)

// SourceTypeCommand is the type of a source whose content is the stdout of a
// command the agent hands to restic (backup --stdin-from-command), rather
// than a path on disk.
const SourceTypeCommand = "command"

// CommandSource is one command-type source of a policy. Name doubles as the
// restic --stdin-filename and as the suffix of the source's own retention
// tag; Command is the shell command line, executed on the agent with the
// same privileges as a hook.
type CommandSource struct {
	Name    string
	Command string
}

// rawSource is the on-wire shape of one entry in a policy's sources JSON
// array, as saved by the GUI.
type rawSource struct {
	Type  string `json:"type"`
	Path  string `json:"path"`
	Label string `json:"label"`
}

// WithoutCommands returns sourcesJSON with the command of every command-type
// source removed, for readers who may see what a policy backs up but not the
// command lines, which often carry database passwords. Entries are decoded
// like CommandSources decodes them, so whatever it treats as a command source
// is redacted; other entries are kept as they are. Input that is not a JSON
// array yields "[]".
func WithoutCommands(sourcesJSON string) string {
	var entries []json.RawMessage
	if err := json.Unmarshal([]byte(sourcesJSON), &entries); err != nil {
		return "[]"
	}
	for i, raw := range entries {
		var s rawSource
		if json.Unmarshal(raw, &s) != nil || s.Type != SourceTypeCommand {
			continue
		}
		redacted, err := json.Marshal(rawSource{Type: SourceTypeCommand, Label: s.Label})
		if err != nil {
			return "[]"
		}
		entries[i] = redacted
	}
	out, err := json.Marshal(entries)
	if err != nil {
		return "[]"
	}
	return string(out)
}

func parseSources(sourcesJSON string) ([]rawSource, error) {
	var sources []rawSource
	if err := json.Unmarshal([]byte(sourcesJSON), &sources); err != nil {
		return nil, fmt.Errorf("invalid sources JSON: %w", err)
	}
	return sources, nil
}

// SourcePaths converts the policy sources JSON (array of source objects saved
// by the GUI) into the flat string array the agent executor expects for the
// regular restic backup invocation. Directory sources become plain paths;
// docker-volume sources become "docker-volume://<volume-name>" URIs.
// Command-type sources are excluded — they are not paths, and are dispatched
// separately as their own restic invocation (see CommandSources).
func SourcePaths(sourcesJSON string) ([]string, error) {
	sources, err := parseSources(sourcesJSON)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(sources))
	for _, s := range sources {
		switch s.Type {
		case SourceTypeCommand:
			continue
		case "docker-volume":
			if s.Path == "" {
				// skip malformed entries to avoid dispatching docker-volume:// with no name
				continue
			}
			paths = append(paths, "docker-volume://"+s.Path)
		default:
			paths = append(paths, s.Path)
		}
	}
	return paths, nil
}

// CommandSources returns the policy's command-type sources, in declaration
// order. Entries with an empty name or command are skipped, mirroring how
// SourcePaths skips a docker-volume entry with no name — Name comes from the
// source's label field, Command from its path field (the same "path's
// meaning is type-dependent" convention docker-volume already established).
func CommandSources(sourcesJSON string) ([]CommandSource, error) {
	sources, err := parseSources(sourcesJSON)
	if err != nil {
		return nil, err
	}
	cmds := make([]CommandSource, 0, len(sources))
	for _, s := range sources {
		if s.Type != SourceTypeCommand {
			continue
		}
		if s.Label == "" || s.Path == "" {
			continue
		}
		cmds = append(cmds, CommandSource{Name: s.Label, Command: s.Path})
	}
	return cmds, nil
}
