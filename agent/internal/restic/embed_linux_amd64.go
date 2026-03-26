//go:build linux && amd64

package restic

import "embed"

//go:embed bin/restic_linux_amd64 bin/rclone_linux_amd64
var embeddedBins embed.FS
