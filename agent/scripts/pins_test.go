package scripts

import (
	"regexp"
	"testing"
)

// TestPinnedVersions checks the versions read from the embedded deps.json,
// which the agent reports to the server on registration.
func TestPinnedVersions(t *testing.T) {
	semver := regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	for name, v := range map[string]string{"restic": ResticVersion, "rclone": RcloneVersion} {
		if !semver.MatchString(v) {
			t.Errorf("%s version = %q, want x.y.z", name, v)
		}
	}
}
