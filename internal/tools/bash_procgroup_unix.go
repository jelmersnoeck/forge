//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

// setProcGroup configures the command to run in its own process group.
// This allows killing the entire tree (bash + children) on cancel/timeout
// instead of orphaning child processes like docker, compilers, etc.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// SIGTERM the entire process group (negative PID).
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}
