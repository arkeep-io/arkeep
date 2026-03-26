//go:build linux && arm64

package restic

import "embed"

//go:embed bin/restic_linux_arm64 bin/rclone_linux_arm64
var embeddedBins embed.FS
