//go:build linux

package testutil

import (
	"os/exec"
	"syscall"
)

// setPdeathsig sets Pdeathsig on Linux so the dolt server is killed
// when the parent test process dies (prevents zombie servers).
func setPdeathsig(cmd *exec.Cmd) {
	if cmd.SysProcAttr != nil {
		cmd.SysProcAttr.Pdeathsig = syscall.SIGTERM
	}
}
