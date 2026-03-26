//go:build windows && amd64

package restic

import "embed"

//go:embed bin/restic_windows_amd64.exe bin/rclone_windows_amd64.exe
var embeddedBins embed.FS
