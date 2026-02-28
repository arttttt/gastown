//go:build windows

package testutil

import (
	"testing"
	"time"
)

func TestParseCSVLine(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple fields",
			input:    "a,b,c",
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "quoted fields",
			input:    `"a","b","c"`,
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "mixed quoted and unquoted",
			input:    `dolt.exe,"1234","Console","1","12345 K","Running","user","0:00:01","N/A","dolt sql-server --data-dir /tmp/test"`,
			expected: []string{"dolt.exe", "1234", "Console", "1", "12345 K", "Running", "user", "0:00:01", "N/A", "dolt sql-server --data-dir /tmp/test"},
		},
		{
			name:     "empty fields",
			input:    "a,,c",
			expected: []string{"a", "", "c"},
		},
		{
			name:     "quoted field with comma",
			input:    `"a,b","c"`,
			expected: []string{"a,b", "c"},
		},
		{
			name:     "single field",
			input:    "single",
			expected: []string{"single"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseCSVLine(tt.input)

			if len(result) != len(tt.expected) {
				t.Errorf("parseCSVLine(%q) returned %d fields, want %d: got %v",
					tt.input, len(result), len(tt.expected), result)
				return
			}

			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("parseCSVLine(%q)[%d] = %q, want %q",
						tt.input, i, result[i], tt.expected[i])
				}
			}
		})
	}
}

func TestReapStaleDoltServers_Windows(t *testing.T) {
	// This test verifies the reapStaleDoltServers function exists and can be called
	// We can't easily test the full functionality without mocking tasklist/wmic

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

// Note: getProcessCreationTime cannot be easily tested without mocking wmic
// The function uses wmic which returns real system data

// Note: parseElapsed is not used on Windows - Windows uses time.Since(creationTime)
// from wmic output instead of parsing elapsed time from ps output.
