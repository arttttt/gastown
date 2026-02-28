//go:build !windows

package testutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/steveyegge/gastown/internal/util"
)

// reapStaleDoltServers finds and kills dolt sql-server processes that:
//   - Have a --data-dir containing "dolt-test-server" (test servers, not production)
//   - Have been running for longer than maxAge
//
// This prevents zombie test servers from accumulating when test processes
// are SIGKILL'd (e.g., go test -timeout expiration) and CleanupDoltServer
// never runs.
func reapStaleDoltServers(maxAge time.Duration) {
	// Use ps to find dolt sql-server processes with test data dirs.
	// Format: PID ELAPSED ARGS
	out, err := exec.Command("ps", "-eo", "pid,etime,args").Output()
	if err != nil {
		return
	}

	var killedPIDs []int
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "dolt sql-server") || !strings.Contains(line, "dolt-test-server") {
			continue
		}
		// Don't kill ourselves (port 3307 = production)
		if strings.Contains(line, "--port 3307") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}
		elapsed := parseElapsed(fields[1])
		if elapsed < maxAge {
			continue
		}
		// Kill the stale test server
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Kill()
			killedPIDs = append(killedPIDs, pid)
		}
	}

	// Clean up PID and lock files for killed processes (or stale files pointing to dead processes)
	cleanupStalePIDFiles(killedPIDs)
}

// cleanupStalePIDFiles removes PID and lock files that match killed PIDs
// or point to processes that are no longer running.
func cleanupStalePIDFiles(killedPIDs []int) {
	tmpDir := os.TempDir()
	pattern := filepath.Join(tmpDir, "dolt-test-server-*.pid")
	pidFiles, err := filepath.Glob(pattern)
	if err != nil {
		return
	}

	killedPIDSet := make(map[int]bool)
	for _, pid := range killedPIDs {
		killedPIDSet[pid] = true
	}

	for _, pidFile := range pidFiles {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			// Can't read, try to remove anyway (file may be corrupted)
			_ = os.Remove(pidFile)
			continue
		}

		lines := strings.SplitN(string(data), "\n", 2)
		if len(lines) == 0 {
			// Empty/corrupted file, remove it
			_ = os.Remove(pidFile)
			continue
		}

		pidStr := strings.TrimSpace(lines[0])
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			// Invalid PID, remove the file
			_ = os.Remove(pidFile)
			continue
		}

		// Remove if we killed this PID, or if the process is no longer running
		if killedPIDSet[pid] || !isProcessRunning(pid) {
			_ = os.Remove(pidFile)
			// Also remove corresponding lock file
			lockFile := strings.TrimSuffix(pidFile, ".pid") + ".lock"
			_ = os.Remove(lockFile)
		}
	}
}

// isProcessRunning returns true if a process with the given PID exists.
func isProcessRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds, so we need to send signal 0 to check
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// parseElapsed converts ps etime format (HH:MM:SS or MM:SS or DD-HH:MM:SS) to duration.
func parseElapsed(s string) time.Duration {
	var days, hours, mins, secs int

	// Handle DD-HH:MM:SS format
	if idx := strings.Index(s, "-"); idx >= 0 {
		fmt.Sscanf(s[:idx], "%d", &days)
		s = s[idx+1:]
	}

	parts := strings.Split(s, ":")
	switch len(parts) {
	case 3:
		fmt.Sscanf(parts[0], "%d", &hours)
		fmt.Sscanf(parts[1], "%d", &mins)
		fmt.Sscanf(parts[2], "%d", &secs)
	case 2:
		fmt.Sscanf(parts[0], "%d", &mins)
		fmt.Sscanf(parts[1], "%d", &secs)
	}

	return time.Duration(days)*24*time.Hour +
		time.Duration(hours)*time.Hour +
		time.Duration(mins)*time.Minute +
		time.Duration(secs)*time.Second
}

func startDoltServer() error {
	// Reap zombie test servers from previous crashed test runs.
	reapStaleDoltServers(5 * time.Minute)

	// Clean up any testdb_* dirs leaked onto the production Dolt data dir
	// by previous test runs (defense-in-depth against stale orphans).
	cleanProductionTestDBs()

	// Determine port: use GT_DOLT_PORT if set externally, otherwise find a free one.
	// GUARD: Never reuse production port 3307 for tests. beads SDK v0.56.x lacks
	// the production port firewall, so tests would create testdb_* databases on the
	// production server and they'd accumulate in .dolt-data/ (gt-l98j).
	if p := os.Getenv("GT_DOLT_PORT"); p != "" && p != "3307" {
		doltTestPort = p
	} else {
		port, err := FindFreePort()
		if err != nil {
			return err
		}
		doltTestPort = strconv.Itoa(port)
		os.Setenv("GT_DOLT_PORT", doltTestPort) //nolint:tenv // intentional process-wide env
		doltPortSetByUs = true
	}

	lockPath := LockFilePathForPort(doltTestPort)
	pidPath := PidFilePathForPort(doltTestPort)

	// Open the lock file (kept open for the lifetime of the test binary).
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0666) //nolint:gosec // test infrastructure
	if err != nil {
		return fmt.Errorf("opening lock file %s: %w", lockPath, err)
	}

	// Acquire exclusive lock for the startup phase.
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		_ = lockFile.Close()
		return fmt.Errorf("acquiring startup lock: %w", err)
	}

	// Under the exclusive lock: check if a server is already running
	// (started by another process that held the lock before us, or external).
	if portReady(2 * time.Second) {
		// Downgrade to shared lock — signals "I'm using the server".
		if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_SH); err != nil {
			_ = lockFile.Close()
			return fmt.Errorf("downgrading to shared lock: %w", err)
		}
		doltLockFile = lockFile
		return nil
	}

	// No server running — start one.
	dataDir, err := os.MkdirTemp("", "dolt-test-server-*")
	if err != nil {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
		return fmt.Errorf("creating dolt data dir: %w", err)
	}

	cmd := exec.CommandContext(context.Background(), "dolt", "sql-server",
		"--port", doltTestPort,
		"--data-dir", dataDir,
	)
	cmd.Stdout = nil
	cmd.Stderr = nil

	// Run in own process group so the entire tree can be killed on cleanup.
	util.SetProcessGroup(cmd)
	// On Linux, also set Pdeathsig so the server dies if the test process is killed.
	// See doltserver_pdeathsig_linux.go.
	setPdeathsig(cmd)

	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dataDir)
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
		return fmt.Errorf("starting dolt sql-server: %w", err)
	}

	// Write PID file so any last-exiting process can clean up.
	// Format: "PID\nDATA_DIR\n"
	pidContent := fmt.Sprintf("%d\n%s\n", cmd.Process.Pid, dataDir)
	if err := os.WriteFile(pidPath, []byte(pidContent), 0666); err != nil { //nolint:gosec // test infrastructure
		_ = cmd.Process.Kill()
		_ = os.RemoveAll(dataDir)
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
		return fmt.Errorf("writing PID file: %w", err)
	}

	// Reap the process in the background so ProcessState is populated on exit.
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()

	// Wait for server to accept connections (up to 30 seconds).
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if portReady(time.Second) {
			// Server is ready. Downgrade to shared lock.
			if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_SH); err != nil {
				_ = lockFile.Close()
				return fmt.Errorf("downgrading to shared lock: %w", err)
			}
			doltLockFile = lockFile
			doltWeStarted = true
			return nil
		}
		// Check if process exited (port bind failure, etc).
		select {
		case <-exited:
			_ = os.RemoveAll(dataDir)
			_ = os.Remove(pidPath)
			_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
			_ = lockFile.Close()
			return fmt.Errorf("dolt sql-server exited prematurely")
		default:
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Timed out — kill and clean up.
	_ = cmd.Process.Kill()
	<-exited
	_ = os.RemoveAll(dataDir)
	_ = os.Remove(pidPath)
	_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	_ = lockFile.Close()
	return fmt.Errorf("dolt sql-server did not become ready within 30s")
}

// CleanupDoltServer conditionally kills the test dolt server. Called from TestMain.
//
// Shutdown protocol: try to upgrade from LOCK_SH to LOCK_EX (non-blocking).
//   - If we get LOCK_EX: no other test processes hold the shared lock, so we're
//     the last user. Read the PID file to find and kill the server.
//   - If LOCK_EX fails (EWOULDBLOCK): another process still holds LOCK_SH,
//     meaning it's actively using the server. Skip cleanup — the last process
//     to exit will handle it.
//
// The PID file enables any last-exiting process to clean up, not just the
// process that originally started the server. This prevents leaked servers
// when the starter exits before other consumers.
func CleanupDoltServer() {
	// Release our shared lock regardless.
	defer func() {
		if doltLockFile != nil {
			_ = syscall.Flock(int(doltLockFile.Fd()), syscall.LOCK_UN)
			_ = doltLockFile.Close()
			doltLockFile = nil
		}
		// Clear GT_DOLT_PORT if we set it, so subsequent processes
		// don't inherit a stale port.
		if doltPortSetByUs {
			_ = os.Unsetenv("GT_DOLT_PORT")
		}
	}()

	if doltLockFile == nil || doltTestPort == "" {
		return
	}

	pidPath := PidFilePathForPort(doltTestPort)

	// Try to acquire exclusive lock (non-blocking). If another process
	// holds LOCK_SH, this fails with EWOULDBLOCK — the server is still in use.
	err := syscall.Flock(int(doltLockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// Another process is using the server. Don't kill it.
		return
	}
	// We got LOCK_EX — we're the last process. Kill from PID file.

	data, err := os.ReadFile(pidPath)
	if err != nil {
		// No PID file — either external server or already cleaned up.
		return
	}

	lines := strings.SplitN(string(data), "\n", 3)
	if len(lines) < 2 {
		return
	}

	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || pid <= 0 {
		return
	}
	dataDir := strings.TrimSpace(lines[1])

	// Kill the server process.
	proc, err := os.FindProcess(pid)
	if err == nil {
		_ = proc.Kill()
		_, _ = proc.Wait()
	}

	// Clean up data dir, PID file, and lock file.
	if dataDir != "" {
		_ = os.RemoveAll(dataDir)
	}
	_ = os.Remove(pidPath)
	_ = os.Remove(LockFilePathForPort(doltTestPort))
}

