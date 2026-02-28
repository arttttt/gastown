//go:build linux

package testutil

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestSetProcessGroup_Linux(t *testing.T) {
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

		// Verify Pdeathsig is SIGTERM (auto-cleanup on parent death)
		if cmd.SysProcAttr.Pdeathsig != syscall.SIGTERM {
			t.Errorf("Pdeathsig should be SIGTERM, got %v", cmd.SysProcAttr.Pdeathsig)
		}
	})

	t.Run("cmd.Cancel is set", func(t *testing.T) {
		cmd := exec.Command("echo", "test")

		setProcessGroup(cmd)

		if cmd.Cancel == nil {
			t.Error("cmd.Cancel should be set")
		}
	})

	t.Run("cancel uses negative PID for process group kill", func(t *testing.T) {
		// Must use CommandContext when setting Cancel
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "sleep", "10")
		setProcessGroup(cmd)

		if err := cmd.Start(); err != nil {
			t.Fatalf("Failed to start command: %v", err)
		}

		// Verify process is running
		if cmd.Process == nil {
			t.Fatal("Process should be set after Start()")
		}

		// Cancel should kill the entire process group using negative PID
		// (we can't easily verify negative PID was used, but the implementation uses -cmd.Process.Pid)
		if err := cmd.Cancel(); err != nil {
			// It's okay if Cancel returns an error (e.g., process already exited)
			t.Logf("Cancel returned: %v", err)
		}

		// Wait for process to exit
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
