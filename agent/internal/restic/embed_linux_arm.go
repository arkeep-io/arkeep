//go:build linux && arm

package restic

import "embed"

//go:embed bin/restic_linux_arm bin/rclone_linux_arm
var embeddedBins embed.FS
