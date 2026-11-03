//go:build windows

package hooks

import (
	"os/exec"
	"strconv"
)

// killProcessTree makes cancelling cmd end the whole process tree rooted at
// cmd.exe, so the processes a hook starts end with it.
func killProcessTree(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	}
}
