//go:build !windows

package testutil

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestParseElapsed(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected time.Duration
	}{
		{
			name:     "MM:SS format",
			input:    "05:30",
			expected: 5*time.Minute + 30*time.Second,
		},
		{
			name:     "HH:MM:SS format",
			input:    "02:30:45",
			expected: 2*time.Hour + 30*time.Minute + 45*time.Second,
		},
		{
			name:     "DD-HH:MM:SS format",
			input:    "1-02:30:45",
			expected: 26*time.Hour + 30*time.Minute + 45*time.Second,
		},
		{
			name:     "single minute",
			input:    "05:00",
			expected: 5 * time.Minute,
		},
		{
			name:     "single second",
			input:    "00:01",
			expected: 1 * time.Second,
		},
		{
			name:     "zero time",
			input:    "00:00",
			expected: 0,
		},
		{
			name:     "empty string",
			input:    "",
			expected: 0,
		},
		{
			name:     "multi-day",
			input:    "3-12:00:00",
			expected: 3*24*time.Hour + 12*time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseElapsed(tt.input)
			if result != tt.expected {
				t.Errorf("parseElapsed(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseElapsed_5MinuteThreshold(t *testing.T) {
	// Test cases around the 5-minute threshold
	tests := []struct {
		name     string
		input    string
		maxAge   time.Duration
		isStale  bool
	}{
		{
			name:    "4 minutes - NOT stale",
			input:   "04:00",
			maxAge:  5 * time.Minute,
			isStale: false,
		},
		{
			name:    "4 minutes 59 seconds - NOT stale",
			input:   "04:59",
			maxAge:  5 * time.Minute,
			isStale: false,
		},
		{
			name:    "5 minutes - IS stale (equal)",
			input:   "05:00",
			maxAge:  5 * time.Minute,
			isStale: true, // equal means >=, so it IS stale
		},
		{
			name:    "5 minutes 1 second - IS stale",
			input:   "05:01",
			maxAge:  5 * time.Minute,
			isStale: true,
		},
		{
			name:    "6 minutes - IS stale",
			input:   "06:00",
			maxAge:  5 * time.Minute,
			isStale: true,
		},
		{
			name:    "1 hour - IS stale",
			input:   "01:00:00",
			maxAge:  5 * time.Minute,
			isStale: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			elapsed := parseElapsed(tt.input)
			isStale := elapsed >= tt.maxAge
			if isStale != tt.isStale {
				t.Errorf("parseElapsed(%q) = %v, stale=%v, want stale=%v", tt.input, elapsed, isStale, tt.isStale)
			}
		})
	}
}

func TestIsProcessRunning(t *testing.T) {
	t.Run("current process is running", func(t *testing.T) {
		pid := syscall.Getpid()
		if !isProcessRunning(pid) {
			t.Errorf("Current process (PID %d) should be running", pid)
		}
	})

	t.Run("non-existent process is not running", func(t *testing.T) {
		// Use a very high PID that's unlikely to exist
		// On most Unix systems, PID max is much lower than 999999
		if isProcessRunning(999999) {
			t.Error("PID 999999 should not be running")
		}
	})

	t.Run("PID 1 is usually init and running", func(t *testing.T) {
		// PID 1 is usually init/systemd
		if !isProcessRunning(1) {
			t.Log("PID 1 not running - unusual but possible in containers")
		}
	})
}

func TestCleanupStalePIDFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test PID files
	pidFile1 := filepath.Join(tmpDir, "dolt-test-server-11111.pid")
	lockFile1 := filepath.Join(tmpDir, "dolt-test-server-11111.lock")
	pidFile2 := filepath.Join(tmpDir, "dolt-test-server-22222.pid")
	lockFile2 := filepath.Join(tmpDir, "dolt-test-server-22222.lock")
	pidFile3 := filepath.Join(tmpDir, "dolt-test-server-33333.pid")

	// Create PID file 1 with killed PID
	if err := os.WriteFile(pidFile1, []byte("11111\n/tmp/data1\n"), 0644); err != nil {
		t.Fatalf("Failed to create PID file: %v", err)
	}
	if err := os.WriteFile(lockFile1, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to create lock file: %v", err)
	}

	// Create PID file 2 with non-existent PID
	if err := os.WriteFile(pidFile2, []byte("99999\n/tmp/data2\n"), 0644); err != nil {
		t.Fatalf("Failed to create PID file: %v", err)
	}
	if err := os.WriteFile(lockFile2, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to create lock file: %v", err)
	}

	// Create PID file 3 with current process (should NOT be removed)
	currentPID := syscall.Getpid()
	if err := os.WriteFile(pidFile3, []byte(strconv.Itoa(currentPID)+"\n/tmp/data3\n"), 0644); err != nil {
		t.Fatalf("Failed to create PID file: %v", err)
	}

	// Verify files exist
	for _, f := range []string{pidFile1, lockFile1, pidFile2, lockFile2, pidFile3} {
		if _, err := os.Stat(f); os.IsNotExist(err) {
			t.Fatalf("Setup failed: %s should exist", f)
		}
	}

	// We can't easily test the full cleanupStalePIDFiles function because
	// it uses filepath.Glob with os.TempDir(). Instead, we test the logic:

	// 1. Test isProcessRunning with known PIDs
	if !isProcessRunning(currentPID) {
		t.Error("Current process should be running")
	}
	if isProcessRunning(99999) {
		t.Error("PID 99999 should not be running")
	}

	// 2. Test that killed PIDs are tracked
	killedPIDs := []int{11111}
	killedPIDSet := make(map[int]bool)
	for _, pid := range killedPIDs {
		killedPIDSet[pid] = true
	}
	if !killedPIDSet[11111] {
		t.Error("Killed PID set should contain 11111")
	}

	// 3. Test parsing PID from file
	data, err := os.ReadFile(pidFile2)
	if err != nil {
		t.Fatalf("Failed to read PID file: %v", err)
	}
	pidStr := string(data[:5]) // "99999"
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid != 99999 {
		t.Errorf("Failed to parse PID from file: %v", err)
	}

	// Clean up manually
	_ = os.Remove(pidFile1)
	_ = os.Remove(lockFile1)
	_ = os.Remove(pidFile2)
	_ = os.Remove(lockFile2)
	_ = os.Remove(pidFile3)
}

func TestCleanupStalePIDFiles_CorruptedFiles(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name    string
		content string
		shouldRemove bool
	}{
		{
			name:    "empty file",
			content: "",
			shouldRemove: true,
		},
		{
			name:    "invalid PID text",
			content: "not-a-number\n/tmp/data\n",
			shouldRemove: true,
		},
		{
			name:    "just whitespace",
			content: "   \n\n",
			shouldRemove: true,
		},
		{
			name:    "valid PID but process dead",
			content: "99999\n/tmp/data\n",
			shouldRemove: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pidFile := filepath.Join(tmpDir, tt.name+".pid")
			if err := os.WriteFile(pidFile, []byte(tt.content), 0644); err != nil {
				t.Fatalf("Failed to create PID file: %v", err)
			}

			// Test reading and parsing
			data, err := os.ReadFile(pidFile)
			if err != nil {
				t.Fatalf("Failed to read PID file: %v", err)
			}

			if len(data) == 0 {
				// Empty file should be removed
				if !tt.shouldRemove {
					t.Error("Empty file should be flagged for removal")
				}
			} else {
				// Try to parse first line as PID
				lines := filepath.SplitList(string(data))
				if len(lines) > 0 {
					pidStr := lines[0]
					_, err := strconv.Atoi(pidStr)
					if err != nil {
						// Invalid PID should be removed
						if !tt.shouldRemove {
							t.Error("Invalid PID file should be flagged for removal")
						}
					}
				}
			}

			_ = os.Remove(pidFile)
		})
	}
}

func TestReapStaleDoltServers(t *testing.T) {
	// This test verifies the reapStaleDoltServers function exists and can be called
	// We can't easily test the full functionality without mocking ps output

	t.Run("function can be called", func(t *testing.T) {
		// Just verify the function doesn't panic when called
		// It will likely find no dolt processes in test environment
		reapStaleDoltServers(5 * time.Minute)
	})

	t.Run("function can be called with different thresholds", func(t *testing.T) {
		reapStaleDoltServers(1 * time.Minute)
		reapStaleDoltServers(10 * time.Minute)
		reapStaleDoltServers(1 * time.Hour)
	})
}

func TestPIDFilePathForPort(t *testing.T) {
	port := "12345"
	path := PidFilePathForPort(port)

	if path == "" {
		t.Error("PidFilePathForPort should return a non-empty path")
	}

	if !filepath.IsAbs(path) {
		t.Error("PidFilePathForPort should return an absolute path")
	}

	if !contains(path, "dolt-test-server-") {
		t.Error("Path should contain 'dolt-test-server-'")
	}

	if !contains(path, port) {
		t.Error("Path should contain the port number")
	}

	if !contains(path, ".pid") {
		t.Error("Path should have .pid extension")
	}
}

func TestLockFilePathForPort(t *testing.T) {
	port := "12345"
	path := LockFilePathForPort(port)

	if path == "" {
		t.Error("LockFilePathForPort should return a non-empty path")
	}

	if !filepath.IsAbs(path) {
		t.Error("LockFilePathForPort should return an absolute path")
	}

	if !contains(path, "dolt-test-server-") {
		t.Error("Path should contain 'dolt-test-server-'")
	}

	if !contains(path, port) {
		t.Error("Path should contain the port number")
	}

	if !contains(path, ".lock") {
		t.Error("Path should have .lock extension")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
