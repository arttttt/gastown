//go:build windows

package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// reapStaleDoltServers finds and kills dolt sql-server processes that:
//   - Have a command line containing "dolt-test-server" (test servers, not production)
//   - Have been running for longer than maxAge
//
// This prevents zombie test servers from accumulating when test processes
// are killed and CleanupDoltServer never runs.
func reapStaleDoltServers(maxAge time.Duration) {
	// Use tasklist with verbose output to get command line and creation time.
	// Format: Image Name, PID, Session Name, Session#, Mem Usage, Status, User Name, CPU Time, Window Title, Command Line
	out, err := exec.Command("tasklist", "/FO", "CSV", "/V", "/FI", "IMAGENAME eq dolt.exe").Output()
	if err != nil {
		// tasklist returns error if no matching processes, which is fine
		return
	}

	lines := strings.Split(string(out), "\n")
	for _, line := range lines[1:] { // Skip header
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse CSV: handle quoted fields
		fields := parseCSVLine(line)
		if len(fields) < 10 {
			continue
		}

		commandLine := fields[9]
		if !strings.Contains(commandLine, "sql-server") || !strings.Contains(commandLine, "dolt-test-server") {
			continue
		}

		// Don't kill production servers
		if strings.Contains(commandLine, "--port 3307") {
			continue
		}

		// Get PID
		pidStr := strings.Trim(fields[1], "\"")
		pid, err := strconv.Atoi(pidStr)
		if err != nil || pid <= 0 {
			continue
		}

		// Get process creation time using wmic
		creationTime, err := getProcessCreationTime(pid)
		if err != nil {
			continue
		}

		age := time.Since(creationTime)
		if age < maxAge {
			continue
		}

		// Kill the stale test server
		_ = exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/F").Run()
	}
}

// getProcessCreationTime returns the creation time of a process using wmic.
func getProcessCreationTime(pid int) (time.Time, error) {
	out, err := exec.Command("wmic", "process", "where", fmt.Sprintf("ProcessId=%d", pid),
		"get", "CreationDate", "/FORMAT:CSV").Output()
	if err != nil {
		return time.Time{}, err
	}

	lines := strings.Split(string(out), "\n")
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) >= 2 {
			// CreationDate format: YYYYMMDDHHMMSS.milliseconds+offset
			dateStr := fields[len(fields)-1]
			if len(dateStr) >= 14 {
				// Parse YYYYMMDDHHMMSS
				t, err := time.Parse("20060102150405", dateStr[:14])
				if err != nil {
					return time.Time{}, err
				}
				return t, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("could not parse creation time")
}

// parseCSVLine parses a simple CSV line handling quoted fields.
func parseCSVLine(line string) []string {
	var fields []string
	var current strings.Builder
	inQuotes := false

	for _, r := range line {
		switch r {
		case '"':
			inQuotes = !inQuotes
		case ',':
			if inQuotes {
				current.WriteRune(r)
			} else {
				fields = append(fields, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	fields = append(fields, current.String())
	return fields
}

func startDoltServer() error {
	// Clean up any testdb_* dirs leaked onto the production Dolt data dir
	// by previous test runs (defense-in-depth against stale orphans).
	cleanProductionTestDBs()

	// Reap zombie test servers from previous crashed test runs.
	reapStaleDoltServers(10 * time.Minute)

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

	pidPath := PidFilePathForPort(doltTestPort)

	// On Windows, skip file locking (syscall.Flock is not available).
	// Check if a server is already running on the port.
	if portReady(2 * time.Second) {
		return nil
	}

	// No server running — start one.
	dataDir, err := os.MkdirTemp("", "dolt-test-server-*")
	if err != nil {
		return fmt.Errorf("creating dolt data dir: %w", err)
	}

	cmd := exec.Command("dolt", "sql-server",
		"--port", doltTestPort,
		"--data-dir", dataDir,
	)
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dataDir)
		return fmt.Errorf("starting dolt sql-server: %w", err)
	}

	// Write PID file so cleanup can find the server.
	pidContent := fmt.Sprintf("%d\n%s\n", cmd.Process.Pid, dataDir)
	if err := os.WriteFile(pidPath, []byte(pidContent), 0666); err != nil { //nolint:gosec // test infrastructure
		_ = cmd.Process.Kill()
		_ = os.RemoveAll(dataDir)
		return fmt.Errorf("writing PID file: %w", err)
	}

	// Reap the process in the background.
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()

	// Wait for server to accept connections (up to 30 seconds).
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if portReady(time.Second) {
			doltWeStarted = true
			return nil
		}
		select {
		case <-exited:
			_ = os.RemoveAll(dataDir)
			_ = os.Remove(pidPath)
			return fmt.Errorf("dolt sql-server exited prematurely")
		default:
		}
		time.Sleep(500 * time.Millisecond)
	}

	_ = cmd.Process.Kill()
	<-exited
	_ = os.RemoveAll(dataDir)
	_ = os.Remove(pidPath)
	return fmt.Errorf("dolt sql-server did not become ready within 30s")
}

// CleanupDoltServer kills the test dolt server on Windows. Called from TestMain.
// On Windows, file locking is not used, so cleanup simply reads the PID file
// and kills the server process.
func CleanupDoltServer() {
	defer func() {
		if doltPortSetByUs {
			_ = os.Unsetenv("GT_DOLT_PORT")
		}
	}()

	if doltTestPort == "" {
		return
	}

	pidPath := PidFilePathForPort(doltTestPort)
	data, err := os.ReadFile(pidPath)
	if err != nil {
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

	proc, err := os.FindProcess(pid)
	if err == nil {
		_ = proc.Kill()
		_, _ = proc.Wait()
	}

	if dataDir != "" {
		_ = os.RemoveAll(dataDir)
	}
	_ = os.Remove(pidPath)
}
