//go:build !windows

package hooks

import (
	"os/exec"
	"syscall"
)

// killProcessTree runs cmd in a process group of its own and makes cancelling
// it kill the whole group, so the processes a hook starts end with it.
func killProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
