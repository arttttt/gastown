//go:build linux

package testutil

import (
	"os/exec"
	"syscall"
)

// setProcessGroup configures the command to run in its own process group so
// that the entire process tree can be killed on cleanup. Pdeathsig ensures
// the server dies if the test process dies (prevents zombie servers).
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid:   true,
		Pdeathsig: syscall.SIGTERM,
	}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			// Negative PID kills the entire process group.
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
}
