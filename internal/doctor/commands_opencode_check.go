package doctor

import (
	"fmt"
	"strings"

	"github.com/steveyegge/gastown/internal/templates"
)

// CommandsOpenCodeCheck validates that town-level .opencode/commands/ is provisioned.
// All agents inherit these via OpenCode's directory traversal.
type CommandsOpenCodeCheck struct {
	FixableCheck
	townRoot        string   // Cached for Fix
	missingCommands []string // Cached during Run for use in Fix
}

// NewCommandsOpenCodeCheck creates a new OpenCode commands check.
func NewCommandsOpenCodeCheck() *CommandsOpenCodeCheck {
	return &CommandsOpenCodeCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "opencode-commands-provisioned",
				CheckDescription: "Check .opencode/commands/ is provisioned at town level",
				CheckCategory:    CategoryConfig,
			},
		},
	}
}

// Run checks if town-level OpenCode slash commands are provisioned.
func (c *CommandsOpenCodeCheck) Run(ctx *CheckContext) *CheckResult {
	c.townRoot = ctx.TownRoot
	c.missingCommands = nil

	missing, err := templates.MissingCommandsOpenCode(ctx.TownRoot)
	if err != nil {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: fmt.Sprintf("Error checking town-level OpenCode commands: %v", err),
		}
	}

	if len(missing) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "Town-level OpenCode slash commands provisioned",
		}
	}

	c.missingCommands = missing
	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: fmt.Sprintf("Missing town-level OpenCode slash commands: %s", strings.Join(missing, ", ")),
		Details: []string{
			fmt.Sprintf("Expected at: %s/.opencode/commands/", ctx.TownRoot),
			"All agents inherit town-level commands via directory traversal",
		},
		FixHint: "Run 'gt doctor --fix' to provision missing commands",
	}
}

// Fix provisions missing OpenCode slash commands at town level.
func (c *CommandsOpenCodeCheck) Fix(ctx *CheckContext) error {
	if len(c.missingCommands) == 0 {
		return nil
	}

	return templates.ProvisionCommandsOpenCode(c.townRoot)
}
