//go:build !windows

package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DoltTestServerLeakCheck detects zombie dolt sql-server processes from
// crashed test runs. These accumulate when test processes are SIGKILL'd
// (e.g., go test -timeout expiration) and CleanupDoltServer never runs.
type DoltTestServerLeakCheck struct {
	FixableCheck
	zombies []doltServerInfo // Cached during Run for use in Fix
}

// doltServerInfo holds information about a zombie dolt server process.
type doltServerInfo struct {
	pid     int
	dataDir string
	age     time.Duration
	command string
}

// NewDoltTestServerLeakCheck creates a new dolt test server leak check.
func NewDoltTestServerLeakCheck() *DoltTestServerLeakCheck {
	return &DoltTestServerLeakCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "dolt-test-server-leaks",
				CheckDescription: "Detect zombie dolt sql-server processes from test runs",
				CheckCategory:    CategoryCleanup,
			},
		},
	}
}

// Run scans for zombie dolt sql-server processes with "dolt-test-server" in args.
func (c *DoltTestServerLeakCheck) Run(ctx *CheckContext) *CheckResult {
	zombies := c.findZombieServers()
	c.zombies = zombies

	if len(zombies) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No zombie dolt test servers found",
		}
	}

	details := make([]string, len(zombies))
	for i, z := range zombies {
		details[i] = fmt.Sprintf("PID %d: running for %s (%s)", z.pid, formatDuration(z.age), z.dataDir)
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: fmt.Sprintf("Found %d zombie dolt test server(s)", len(zombies)),
		Details: details,
		FixHint: "Run 'gt doctor --fix' to kill zombie servers and clean up data directories",
	}
}

// Fix kills all zombie dolt test servers (SIGTERM then SIGKILL) and cleans up
// their data directories and PID/lock files.
func (c *DoltTestServerLeakCheck) Fix(ctx *CheckContext) error {
	if len(c.zombies) == 0 {
		return nil
	}

	var lastErr error
	for _, z := range c.zombies {
		// Try graceful shutdown first (SIGTERM), then poll for exit.
		_ = syscall.Kill(z.pid, syscall.SIGTERM)
		if !waitForProcessExit(z.pid, 3*time.Second) {
			// Still running — force kill.
			if proc, err := os.FindProcess(z.pid); err == nil {
				_ = proc.Kill()
				_, _ = proc.Wait()
			}
		}

		// Clean up data directory
		if z.dataDir != "" {
			if err := os.RemoveAll(z.dataDir); err != nil {
				lastErr = err
			}
		}

		// Clean up PID and lock files for this server
		c.cleanupPIDFiles(z)
	}

	return lastErr
}

// cleanupPIDFiles removes PID and lock files associated with a zombie server.
func (c *DoltTestServerLeakCheck) cleanupPIDFiles(z doltServerInfo) {
	tmpDir := os.TempDir()
	pattern := filepath.Join(tmpDir, "dolt-test-server-*.pid")
	pidFiles, _ := filepath.Glob(pattern)

	for _, pidFile := range pidFiles {
		// Read PID file and check if it matches our zombie
		data, err := os.ReadFile(pidFile)
		if err != nil {
			continue
		}

		lines := strings.SplitN(string(data), "\n", 2)
		if len(lines) > 0 {
			pidStr := strings.TrimSpace(lines[0])
			if pid, err := strconv.Atoi(pidStr); err == nil && pid == z.pid {
				// This PID file belongs to our zombie, remove it and corresponding lock file
				_ = os.Remove(pidFile)

				// Derive lock file path from PID file path
				lockFile := strings.TrimSuffix(pidFile, ".pid") + ".lock"
				_ = os.Remove(lockFile)
			}
		}
	}
}

// findZombieServers finds dolt sql-server processes with "dolt-test-server" in args.
func (c *DoltTestServerLeakCheck) findZombieServers() []doltServerInfo {
	var zombies []doltServerInfo

	// Use ps to find dolt sql-server processes
	out, err := exec.Command("ps", "-eo", "pid,etime,args").Output()
	if err != nil {
		return zombies
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "dolt sql-server") || !strings.Contains(line, "dolt-test-server") {
			continue
		}

		// Don't flag production servers
		if strings.Contains(line, "--port 3307") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}

		// Find data dir from command line args
		dataDir := extractDataDir(strings.Join(fields[2:], " "))

		// Parse elapsed time
		elapsed := parseElapsed(fields[1])

		zombies = append(zombies, doltServerInfo{
			pid:     pid,
			dataDir: dataDir,
			age:     elapsed,
			command: strings.Join(fields[2:], " "),
		})
	}

	return zombies
}

// extractDataDir extracts the --data-dir value from command line args.
func extractDataDir(args string) string {
	parts := strings.Fields(args)
	for i, part := range parts {
		if part == "--data-dir" && i+1 < len(parts) {
			return parts[i+1]
		}
		if strings.HasPrefix(part, "--data-dir=") {
			return strings.TrimPrefix(part, "--data-dir=")
		}
	}
	return ""
}

// parseElapsed converts ps etime format to duration.
// Handles: HH:MM:SS, MM:SS, DD-HH:MM:SS
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

// waitForProcessExit polls until the process exits or timeout is reached.
func waitForProcessExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		proc, err := os.FindProcess(pid)
		if err != nil {
			return true
		}
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return true // process is gone
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
