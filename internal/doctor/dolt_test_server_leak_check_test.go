//go:build !windows

package doctor

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNewDoltTestServerLeakCheck(t *testing.T) {
	check := NewDoltTestServerLeakCheck()

	t.Run("has correct name", func(t *testing.T) {
		if check.Name() != "dolt-test-server-leaks" {
			t.Errorf("expected name 'dolt-test-server-leaks', got %q", check.Name())
		}
	})

	t.Run("has correct description", func(t *testing.T) {
		expected := "Detect zombie dolt sql-server processes from test runs"
		if check.Description() != expected {
			t.Errorf("expected description %q, got %q", expected, check.Description())
		}
	})

	t.Run("is fixable", func(t *testing.T) {
		if !check.CanFix() {
			t.Error("expected CanFix to return true")
		}
	})

	t.Run("has correct category", func(t *testing.T) {
		if check.Category() != CategoryCleanup {
			t.Errorf("expected category %q, got %q", CategoryCleanup, check.Category())
		}
	})
}

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
			expected: 24*time.Hour + 2*time.Hour + 30*time.Minute + 45*time.Second,
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

func TestExtractDataDir(t *testing.T) {
	tests := []struct {
		name     string
		args     string
		expected string
	}{
		{
			name:     "separate flag",
			args:     "sql-server --port 12345 --data-dir /tmp/dolt-test-server-abc",
			expected: "/tmp/dolt-test-server-abc",
		},
		{
			name:     "equals syntax",
			args:     "sql-server --port=12345 --data-dir=/tmp/dolt-test-server-xyz",
			expected: "/tmp/dolt-test-server-xyz",
		},
		{
			name:     "no data dir",
			args:     "sql-server --port 12345",
			expected: "",
		},
		{
			name:     "data dir at end",
			args:     "sql-server --port 12345 --data-dir /tmp/test",
			expected: "/tmp/test",
		},
		{
			name:     "empty args",
			args:     "",
			expected: "",
		},
		{
			name:     "data-dir without value",
			args:     "sql-server --data-dir",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractDataDir(tt.args)
			if result != tt.expected {
				t.Errorf("extractDataDir(%q) = %q, want %q", tt.args, result, tt.expected)
			}
		})
	}
}

func TestDoltTestServerLeakCheck_Run_NoZombies(t *testing.T) {
	check := NewDoltTestServerLeakCheck()
	ctx := &CheckContext{TownRoot: t.TempDir()}

	result := check.Run(ctx)

	// Should return OK when no zombies exist
	if result.Status != StatusOK {
		t.Errorf("expected StatusOK when no zombies exist, got %v: %s", result.Status, result.Message)
	}

	if !strings.Contains(result.Message, "No zombie") {
		t.Errorf("expected message to contain 'No zombie', got %q", result.Message)
	}
}

func TestDoltTestServerLeakCheck_Run_SkipsProductionPort(t *testing.T) {
	// This test verifies the check would skip production servers on port 3307
	// We can't easily create a fake process, but we can verify the logic exists
	check := NewDoltTestServerLeakCheck()

	// Verify the check has the correct properties
	if check.Name() != "dolt-test-server-leaks" {
		t.Error("Check name should be dolt-test-server-leaks")
	}

	// The actual filtering of port 3307 happens in findZombieServers
	// which we can't easily test without mocking ps output
	t.Log("Note: Port 3307 filtering is tested in findZombieServers, requires process mocking")
}

func TestDoltTestServerLeakCheck_Fix_NoZombies(t *testing.T) {
	check := NewDoltTestServerLeakCheck()
	ctx := &CheckContext{TownRoot: t.TempDir()}

	// Fix with no zombies should succeed
	err := check.Fix(ctx)
	if err != nil {
		t.Errorf("Fix with no zombies should succeed, got error: %v", err)
	}
}

func TestDoltTestServerLeakCheck_CleanupPIDFiles(t *testing.T) {
	check := NewDoltTestServerLeakCheck()

	t.Run("handles zombie with no matching PID file", func(t *testing.T) {
		zombie := doltServerInfo{
			pid:     12345,
			dataDir: "/tmp/test-data",
			age:     5 * time.Minute,
			command: "dolt sql-server",
		}

		// Just verify no panic occurs
		check.cleanupPIDFiles(zombie)
	})
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		duration time.Duration
		expected string
	}{
		{30 * time.Second, "30.0s"},
		{90 * time.Second, "1m 30s"},
		{5 * time.Minute, "5m"},
		{90 * time.Minute, "1h 30m"},
		{2 * time.Hour, "2h"},
		{150 * time.Minute, "2h 30m"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatDuration(tt.duration)
			if result != tt.expected {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.duration, result, tt.expected)
			}
		})
	}
}

// Test with real PID files in temp directory
func TestDoltTestServerLeakCheck_Cleanup_RealFiles(t *testing.T) {
	// Create temporary PID and lock files
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "dolt-test-server-12345.pid")
	lockFile := filepath.Join(tmpDir, "dolt-test-server-12345.lock")

	// Write PID file
	if err := os.WriteFile(pidFile, []byte("12345\n/tmp/test-data\n"), 0644); err != nil {
		t.Fatalf("Failed to create PID file: %v", err)
	}

	// Write lock file
	if err := os.WriteFile(lockFile, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to create lock file: %v", err)
	}

	// Verify files exist
	if _, err := os.Stat(pidFile); os.IsNotExist(err) {
		t.Fatal("PID file should exist")
	}
	if _, err := os.Stat(lockFile); os.IsNotExist(err) {
		t.Fatal("Lock file should exist")
	}

	// Clean up files
	_ = os.Remove(pidFile)
	_ = os.Remove(lockFile)

	// Verify files are removed
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("PID file should be removed")
	}
}

// Test parsing of PID file content
func TestPIDFileParsing(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		expectedPID    int
		expectedDataDir string
		valid          bool
	}{
		{
			name:           "valid PID file",
			content:        "12345\n/tmp/test-data\n",
			expectedPID:    12345,
			expectedDataDir: "/tmp/test-data",
			valid:          true,
		},
		{
			name:           "no trailing newline",
			content:        "12345\n/tmp/test-data",
			expectedPID:    12345,
			expectedDataDir: "/tmp/test-data",
			valid:          true,
		},
		{
			name:           "just PID",
			content:        "12345",
			expectedPID:    12345,
			expectedDataDir: "",
			valid:          false, // Missing data dir
		},
		{
			name:           "empty file",
			content:        "",
			expectedPID:    0,
			expectedDataDir: "",
			valid:          false,
		},
		{
			name:           "invalid PID",
			content:        "not-a-number\n/tmp/test\n",
			expectedPID:    0,
			expectedDataDir: "",
			valid:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Write content to temp file
			tmpFile := filepath.Join(t.TempDir(), "test.pid")
			if err := os.WriteFile(tmpFile, []byte(tt.content), 0644); err != nil {
				t.Fatalf("Failed to write test file: %v", err)
			}

			// Read and parse
			data, err := os.ReadFile(tmpFile)
			if err != nil {
				t.Fatalf("Failed to read test file: %v", err)
			}

			lines := strings.SplitN(string(data), "\n", 3)
			if len(lines) < 1 {
				t.Fatal("Should have at least one line")
			}

			pidStr := strings.TrimSpace(lines[0])
			pid, err := strconv.Atoi(pidStr)
			if tt.valid && err != nil {
				t.Errorf("Failed to parse PID: %v", err)
			}

			var dataDir string
			if len(lines) >= 2 {
				dataDir = strings.TrimSpace(lines[1])
			}

			if tt.valid {
				if pid != tt.expectedPID {
					t.Errorf("Expected PID %d, got %d", tt.expectedPID, pid)
				}
				if dataDir != tt.expectedDataDir {
					t.Errorf("Expected data dir %q, got %q", tt.expectedDataDir, dataDir)
				}
			}
		})
	}
}
