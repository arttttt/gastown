//go:build windows

package doctor

import (
	"testing"
)

func TestNewDoltTestServerLeakCheck_Windows(t *testing.T) {
	check := NewDoltTestServerLeakCheck()

	t.Run("has correct name on Windows", func(t *testing.T) {
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

	t.Run("is not fixable on Windows", func(t *testing.T) {
		// Windows version returns CanFix = true (inherited from FixableCheck),
		// but Fix() is a no-op
		if !check.CanFix() {
			t.Error("Windows check should report CanFix = true for consistency")
		}
	})

	t.Run("has correct category", func(t *testing.T) {
		if check.Category() != CategoryCleanup {
			t.Errorf("expected category %q, got %q", CategoryCleanup, check.Category())
		}
	})
}

func TestDoltTestServerLeakCheck_Run_Windows(t *testing.T) {
	check := NewDoltTestServerLeakCheck()
	ctx := &CheckContext{TownRoot: t.TempDir()}

	result := check.Run(ctx)

	// Windows version always returns OK (reaping is handled by testutil)
	if result.Status != StatusOK {
		t.Errorf("expected StatusOK on Windows, got %v: %s", result.Status, result.Message)
	}

	expectedMsg := "Skipped on Windows"
	if result.Message != expectedMsg && result.Message != "Skipped on Windows (handled by test infrastructure)" {
		t.Errorf("expected message to indicate Windows skip, got %q", result.Message)
	}
}

func TestDoltTestServerLeakCheck_Fix_Windows(t *testing.T) {
	check := NewDoltTestServerLeakCheck()
	ctx := &CheckContext{TownRoot: t.TempDir()}

	// Fix on Windows should always succeed (no-op)
	err := check.Fix(ctx)
	if err != nil {
		t.Errorf("Fix on Windows should always succeed, got error: %v", err)
	}
}
