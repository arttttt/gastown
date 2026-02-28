//go:build !windows

package testutil

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
		{"MM:SS", "05:30", 5*time.Minute + 30*time.Second},
		{"HH:MM:SS", "02:30:45", 2*time.Hour + 30*time.Minute + 45*time.Second},
		{"DD-HH:MM:SS", "1-02:30:45", 26*time.Hour + 30*time.Minute + 45*time.Second},
		{"single minute", "05:00", 5 * time.Minute},
		{"single second", "00:01", 1 * time.Second},
		{"zero", "00:00", 0},
		{"empty", "", 0},
		{"multi-day", "3-12:00:00", 3*24*time.Hour + 12*time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseElapsed(tt.input); got != tt.expected {
				t.Errorf("parseElapsed(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestParseElapsed_5MinuteThreshold(t *testing.T) {
	tests := []struct {
		input   string
		isStale bool
	}{
		{"04:00", false},
		{"04:59", false},
		{"05:00", true},  // exactly 5 min — stale (>=)
		{"05:01", true},
		{"06:00", true},
		{"01:00:00", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			elapsed := parseElapsed(tt.input)
			if got := elapsed >= 5*time.Minute; got != tt.isStale {
				t.Errorf("parseElapsed(%q)=%v >= 5m = %v, want %v", tt.input, elapsed, got, tt.isStale)
			}
		})
	}
}

func TestIsProcessRunning(t *testing.T) {
	t.Run("self is running", func(t *testing.T) {
		if !isProcessRunning(syscall.Getpid()) {
			t.Error("current process should be running")
		}
	})

	t.Run("bogus PID is not running", func(t *testing.T) {
		if isProcessRunning(999999) {
			t.Error("PID 999999 should not be running")
		}
	})
}

func TestCleanupStalePIDFiles(t *testing.T) {
	// Override TmpDir so cleanupStalePIDFiles finds our test files.
	origTmpDir := os.Getenv("TMPDIR")
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)
	defer os.Setenv("TMPDIR", origTmpDir)

	// PID file for a killed PID (should be removed)
	pidFile1 := filepath.Join(tmpDir, "dolt-test-server-11111.pid")
	lockFile1 := filepath.Join(tmpDir, "dolt-test-server-11111.lock")
	os.WriteFile(pidFile1, []byte("11111\n/tmp/data1\n"), 0644)
	os.WriteFile(lockFile1, []byte(""), 0644)

	// PID file for a dead process (should be removed)
	pidFile2 := filepath.Join(tmpDir, "dolt-test-server-99999.pid")
	lockFile2 := filepath.Join(tmpDir, "dolt-test-server-99999.lock")
	os.WriteFile(pidFile2, []byte("99999\n/tmp/data2\n"), 0644)
	os.WriteFile(lockFile2, []byte(""), 0644)

	// PID file for current process (should NOT be removed)
	myPID := syscall.Getpid()
	pidFile3 := filepath.Join(tmpDir, "dolt-test-server-33333.pid")
	os.WriteFile(pidFile3, []byte(strconv.Itoa(myPID)+"\n/tmp/data3\n"), 0644)

	cleanupStalePIDFiles([]int{11111})

	// killed PID — both files removed
	if _, err := os.Stat(pidFile1); !os.IsNotExist(err) {
		t.Error("PID file for killed process should be removed")
	}
	if _, err := os.Stat(lockFile1); !os.IsNotExist(err) {
		t.Error("lock file for killed process should be removed")
	}

	// dead process (99999) — both files removed
	if _, err := os.Stat(pidFile2); !os.IsNotExist(err) {
		t.Error("PID file for dead process should be removed")
	}
	if _, err := os.Stat(lockFile2); !os.IsNotExist(err) {
		t.Error("lock file for dead process should be removed")
	}

	// current process — file should survive
	if _, err := os.Stat(pidFile3); os.IsNotExist(err) {
		t.Error("PID file for running process should NOT be removed")
	}
}

func TestCleanupStalePIDFiles_CorruptedFiles(t *testing.T) {
	origTmpDir := os.Getenv("TMPDIR")
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)
	defer os.Setenv("TMPDIR", origTmpDir)

	tests := []struct {
		name    string
		content string
	}{
		{"empty", ""},
		{"invalid PID", "not-a-number\n/tmp/data\n"},
		{"whitespace only", "   \n\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := filepath.Join(tmpDir, "dolt-test-server-"+strings.ReplaceAll(tt.name, " ", "")+".pid")
			os.WriteFile(f, []byte(tt.content), 0644)

			cleanupStalePIDFiles(nil) // no killed PIDs, relies on isProcessRunning

			if _, err := os.Stat(f); !os.IsNotExist(err) {
				t.Errorf("corrupted PID file %q should be removed", tt.name)
			}
		})
	}
}

func TestReapStaleDoltServers_NoPanic(t *testing.T) {
	// Smoke test: calling reap with various thresholds must not panic.
	reapStaleDoltServers(5 * time.Minute)
	reapStaleDoltServers(1 * time.Minute)
	reapStaleDoltServers(1 * time.Hour)
}

func TestPIDFilePathForPort(t *testing.T) {
	path := PidFilePathForPort("12345")
	if !filepath.IsAbs(path) {
		t.Error("should be absolute path")
	}
	if !strings.Contains(path, "dolt-test-server-") || !strings.Contains(path, "12345") || !strings.Contains(path, ".pid") {
		t.Errorf("unexpected path format: %s", path)
	}
}

func TestLockFilePathForPort(t *testing.T) {
	path := LockFilePathForPort("12345")
	if !filepath.IsAbs(path) {
		t.Error("should be absolute path")
	}
	if !strings.Contains(path, "dolt-test-server-") || !strings.Contains(path, "12345") || !strings.Contains(path, ".lock") {
		t.Errorf("unexpected path format: %s", path)
	}
}
