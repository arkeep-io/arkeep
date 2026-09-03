//go:build linux && mipsle

package restic

import "embed"

//go:embed bin/restic_linux_mipsle bin/rclone_linux_mipsle
var embeddedBins embed.FS
