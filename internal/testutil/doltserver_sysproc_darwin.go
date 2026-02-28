//go:build darwin

package testutil

import (
	"os/exec"
	"syscall"
)

// setProcessGroup configures the command to run in its own process group.
// This implementation is for Darwin/macOS which doesn't support Pdeathsig.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			// Negative PID kills the entire process group.
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
}
