//go:build windows

package analyze

import "os/exec"

// killProcessGroup is a no-op on Windows; exec.CommandContext kills the
// process and WaitDelay bounds the wait for any children holding its output.
func killProcessGroup(cmd *exec.Cmd) {}
