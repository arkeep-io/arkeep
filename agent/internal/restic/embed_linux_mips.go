//go:build linux && mips

package restic

import "embed"

//go:embed bin/restic_linux_mips bin/rclone_linux_mips
var embeddedBins embed.FS
