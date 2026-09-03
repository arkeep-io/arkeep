// Package policyutil provides helpers for interpreting backup policy fields.
// SourcePaths is shared between the scheduler (backup dispatch) and the API
// layer (policy validation) so both operate on the exact same flattened
// source list — the API validates precisely what the scheduler will later
// send to the agent.
package policyutil

import (
	"encoding/json"
	"fmt"
)

// SourcePaths converts the policy sources JSON (array of source objects saved
// by the GUI) into the flat string array the agent executor expects.
// Directory sources become plain paths; docker-volume sources become
// "docker-volume://<volume-name>" URIs.
func SourcePaths(sourcesJSON string) ([]string, error) {
	var sources []struct {
		Type string `json:"type"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(sourcesJSON), &sources); err != nil {
		return nil, fmt.Errorf("invalid sources JSON: %w", err)
	}
	paths := make([]string, 0, len(sources))
	for _, s := range sources {
		if s.Type == "docker-volume" {
			if s.Path == "" {
				// skip malformed entries to avoid dispatching docker-volume:// with no name
				continue
			}
			paths = append(paths, "docker-volume://"+s.Path)
		} else {
			paths = append(paths, s.Path)
		}
	}
	return paths, nil
}
