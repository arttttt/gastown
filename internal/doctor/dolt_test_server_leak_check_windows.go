//go:build windows

package doctor

// DoltTestServerLeakCheck is a no-op on Windows.
// Windows uses the reapStaleDoltServers function in testutil instead.
type DoltTestServerLeakCheck struct {
	FixableCheck
}

// NewDoltTestServerLeakCheck creates a new dolt test server leak check (no-op on Windows).
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

// Run always returns OK on Windows (reaping is handled by testutil).
func (c *DoltTestServerLeakCheck) Run(ctx *CheckContext) *CheckResult {
	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusOK,
		Message: "Skipped on Windows (handled by test infrastructure)",
	}
}

// Fix is a no-op on Windows.
func (c *DoltTestServerLeakCheck) Fix(ctx *CheckContext) error {
	return nil
}
