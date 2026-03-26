//go:build darwin && amd64

package restic

import "embed"

//go:embed bin/restic_darwin_amd64 bin/rclone_darwin_amd64
var embeddedBins embed.FS
