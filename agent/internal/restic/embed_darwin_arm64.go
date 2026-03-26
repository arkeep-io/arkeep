//go:build darwin && arm64

package restic

import "embed"

//go:embed bin/restic_darwin_arm64 bin/rclone_darwin_arm64
var embeddedBins embed.FS
