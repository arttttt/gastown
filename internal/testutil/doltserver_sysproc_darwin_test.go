//go:build darwin

package testutil

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestSetProcessGroup_Darwin(t *testing.T) {
	t.Run("sets correct SysProcAttr", func(t *testing.T) {
		cmd := exec.Command("echo", "test")

		setProcessGroup(cmd)

		if cmd.SysProcAttr == nil {
			t.Fatal("SysProcAttr should be set")
		}

		// Verify Setpgid is true (creates new process group)
		if !cmd.SysProcAttr.Setpgid {
			t.Error("Setpgid should be true")
		}

		// Darwin doesn't support Pdeathsig - the field doesn't exist in syscall.SysProcAttr
		// This is the expected behavior per the implementation in doltserver_sysproc_darwin.go
	})

	t.Run("cmd.Cancel is set", func(t *testing.T) {
		cmd := exec.Command("echo", "test")

		setProcessGroup(cmd)

		if cmd.Cancel == nil {
			t.Error("cmd.Cancel should be set")
		}
	})

	t.Run("cancel kills process group", func(t *testing.T) {
		// Must use CommandContext when setting Cancel
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "sleep", "10")
		setProcessGroup(cmd)

		if err := cmd.Start(); err != nil {
			t.Fatalf("Failed to start command: %v", err)
		}

		if cmd.Process == nil {
			t.Fatal("Process should be set after Start()")
		}

		// Cancel should kill the process
		if err := cmd.Cancel(); err != nil {
			t.Logf("Cancel returned: %v", err)
		}

		_ = cmd.Wait()
	})

	t.Run("process runs in separate process group", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "sleep", "10")
		setProcessGroup(cmd)

		if err := cmd.Start(); err != nil {
			t.Fatalf("Failed to start command: %v", err)
		}

		defer func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}()

		// Get the PGID of the child process
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err != nil {
			t.Fatalf("Failed to get pgid: %v", err)
		}

		// The child's PGID should be its own PID (new process group)
		if pgid != cmd.Process.Pid {
			t.Errorf("Child should be in its own process group: expected PGID=%d, got PGID=%d", cmd.Process.Pid, pgid)
		}

		// The child's PGID should be different from parent's PGID
		parentPgid, err := syscall.Getpgid(syscall.Getpid())
		if err != nil {
			t.Fatalf("Failed to get parent pgid: %v", err)
		}

		if pgid == parentPgid {
			t.Error("Child process group should be different from parent")
		}
	})
}
