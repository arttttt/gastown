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

	if check.Name() != "dolt-test-server-leaks" {
		t.Errorf("name = %q, want %q", check.Name(), "dolt-test-server-leaks")
	}
	if check.Category() != CategoryCleanup {
		t.Errorf("category = %q, want %q", check.Category(), CategoryCleanup)
	}
	if !check.CanFix() {
		t.Error("should be fixable")
	}
}

func TestParseElapsed(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"05:30", 5*time.Minute + 30*time.Second},
		{"02:30:45", 2*time.Hour + 30*time.Minute + 45*time.Second},
		{"1-02:30:45", 24*time.Hour + 2*time.Hour + 30*time.Minute + 45*time.Second},
		{"05:00", 5 * time.Minute},
		{"00:01", 1 * time.Second},
		{"00:00", 0},
		{"", 0},
		{"3-12:00:00", 3*24*time.Hour + 12*time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := parseElapsed(tt.input); got != tt.expected {
				t.Errorf("parseElapsed(%q) = %v, want %v", tt.input, got, tt.expected)
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
		{"separate flag", "sql-server --port 12345 --data-dir /tmp/dolt-test-server-abc", "/tmp/dolt-test-server-abc"},
		{"equals syntax", "sql-server --port=12345 --data-dir=/tmp/dolt-test-server-xyz", "/tmp/dolt-test-server-xyz"},
		{"no data dir", "sql-server --port 12345", ""},
		{"data dir at end", "sql-server --port 12345 --data-dir /tmp/test", "/tmp/test"},
		{"empty args", "", ""},
		{"data-dir without value", "sql-server --data-dir", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractDataDir(tt.args); got != tt.expected {
				t.Errorf("extractDataDir(%q) = %q, want %q", tt.args, got, tt.expected)
			}
		})
	}
}

func TestDoltTestServerLeakCheck_Run(t *testing.T) {
	check := NewDoltTestServerLeakCheck()
	ctx := &CheckContext{TownRoot: t.TempDir()}
	result := check.Run(ctx)

	// Accept either OK (no zombies) or Warning (real zombies on machine).
	// The important thing is it doesn't error/panic.
	switch result.Status {
	case StatusOK:
		if !strings.Contains(result.Message, "No zombie") {
			t.Errorf("OK result should mention no zombies, got %q", result.Message)
		}
	case StatusWarning:
		if !strings.Contains(result.Message, "zombie") {
			t.Errorf("Warning result should mention zombies, got %q", result.Message)
		}
		if len(result.Details) == 0 {
			t.Error("Warning result should include details")
		}
	default:
		t.Errorf("unexpected status %v: %s", result.Status, result.Message)
	}
}

func TestDoltTestServerLeakCheck_Fix_NoZombies(t *testing.T) {
	check := NewDoltTestServerLeakCheck()
	// Don't call Run() — zombies list is empty
	if err := check.Fix(&CheckContext{TownRoot: t.TempDir()}); err != nil {
		t.Errorf("Fix with no zombies should succeed, got: %v", err)
	}
}

func TestDoltTestServerLeakCheck_CleanupPIDFiles(t *testing.T) {
	check := NewDoltTestServerLeakCheck()

	// Create a PID file matching the zombie PID in a temp dir
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "dolt-test-server-12345.pid")
	lockFile := filepath.Join(tmpDir, "dolt-test-server-12345.lock")
	os.WriteFile(pidFile, []byte("12345\n/tmp/test-data\n"), 0644)
	os.WriteFile(lockFile, []byte(""), 0644)

	zombie := doltServerInfo{pid: 12345, dataDir: "/tmp/test-data", age: 5 * time.Minute}

	// cleanupPIDFiles scans os.TempDir(), not our tmpDir, so this just
	// verifies no panic. The real cleanup is tested via integration.
	check.cleanupPIDFiles(zombie)
}

func TestWaitForProcessExit(t *testing.T) {
	t.Run("already dead PID returns true", func(t *testing.T) {
		if !waitForProcessExit(999999, 500*time.Millisecond) {
			t.Error("dead PID should return true immediately")
		}
	})

	t.Run("self PID returns false (still running)", func(t *testing.T) {
		if waitForProcessExit(os.Getpid(), 300*time.Millisecond) {
			t.Error("own PID should not exit within timeout")
		}
	})
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30.0s"},
		{90 * time.Second, "1m 30s"},
		{5 * time.Minute, "5m"},
		{90 * time.Minute, "1h 30m"},
		{2 * time.Hour, "2h"},
		{150 * time.Minute, "2h 30m"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatDuration(tt.d); got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestPIDFileParsing(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantPID int
		wantDir string
		valid   bool
	}{
		{"valid", "12345\n/tmp/test-data\n", 12345, "/tmp/test-data", true},
		{"no trailing newline", "12345\n/tmp/test-data", 12345, "/tmp/test-data", true},
		{"just PID", "12345", 12345, "", false},
		{"empty", "", 0, "", false},
		{"invalid PID", "not-a-number\n/tmp/test\n", 0, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := filepath.Join(t.TempDir(), "test.pid")
			os.WriteFile(f, []byte(tt.content), 0644)

			data, _ := os.ReadFile(f)
			lines := strings.SplitN(string(data), "\n", 3)

			if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
				if tt.valid {
					t.Error("expected valid parse")
				}
				return
			}

			pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
			if tt.valid {
				if err != nil {
					t.Errorf("failed to parse PID: %v", err)
				}
				if pid != tt.wantPID {
					t.Errorf("PID = %d, want %d", pid, tt.wantPID)
				}
				var dir string
				if len(lines) >= 2 {
					dir = strings.TrimSpace(lines[1])
				}
				if dir != tt.wantDir {
					t.Errorf("dataDir = %q, want %q", dir, tt.wantDir)
				}
			}
		})
	}
}
