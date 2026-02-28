//go:build !linux && !windows

package testutil

import "os/exec"

// setPdeathsig is a no-op on non-Linux Unix systems (e.g., macOS)
// where Pdeathsig is not available.
func setPdeathsig(_ *exec.Cmd) {}
