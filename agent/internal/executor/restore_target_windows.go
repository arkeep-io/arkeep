//go:build windows

package executor

// secureRestoreTarget is not implemented on Windows yet: protecting the folder
// means setting its ACL, which has not been tried on a Windows machine. The
// default target there, C:\Users\Public\arkeep-restore, stays readable by
// every local user (P-03, Windows part, still open).
func secureRestoreTarget(string) error {
	return nil
}
