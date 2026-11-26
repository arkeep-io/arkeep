// Package scripts exposes the restic and rclone versions pinned in deps.json,
// the file download_deps.go reads to fetch the binaries the agent embeds. The
// scripts themselves are built only with `go run` (//go:build ignore).
package scripts

import (
	_ "embed"
	"encoding/json"
)

//go:embed deps.json
var depsJSON []byte

// ResticVersion and RcloneVersion are the versions of the embedded restic and
// rclone binaries (e.g. "0.19.1", "1.76.0").
var ResticVersion, RcloneVersion = pinnedVersions(depsJSON)

// pinnedVersions reads the restic and rclone versions from deps.json. It
// panics on a malformed file: deps.json is embedded at build time, so that can
// only be a build error.
func pinnedVersions(data []byte) (restic, rclone string) {
	var pins struct {
		Restic struct {
			Version string `json:"version"`
		} `json:"restic"`
		Rclone struct {
			Version string `json:"version"`
		} `json:"rclone"`
	}
	if err := json.Unmarshal(data, &pins); err != nil {
		panic("scripts: invalid deps.json: " + err.Error())
	}
	return pins.Restic.Version, pins.Rclone.Version
}
