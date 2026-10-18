//go:build !windows

package analyze

import (
	"os/exec"
	"syscall"
)

// killProcessGroup starts cmd in its own process group and, on cancel,
// kills the group so plugins and other children die with it.
func killProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
