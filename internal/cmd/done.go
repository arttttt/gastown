package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/mail"
	"github.com/steveyegge/gastown/internal/mrqueue"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var doneCmd = &cobra.Command{
	Use:     "done",
	GroupID: GroupWork,
	Short:   "Signal work ready for merge queue",
	Long: `Signal that your work is complete and ready for the merge queue.

This is a convenience command for polecats that:
1. Submits the current branch to the merge queue
2. Auto-detects issue ID from branch name
3. Notifies the Witness with the exit outcome

Exit types:
  COMPLETED  - Work done, MR submitted (default)
  ESCALATED  - Hit blocker, needs human intervention
  DEFERRED   - Work paused, issue still open

Examples:
  gt done                       # Submit branch, notify COMPLETED
  gt done --issue gt-abc        # Explicit issue ID
  gt done --exit ESCALATED      # Signal blocker, skip MR
  gt done --exit DEFERRED       # Pause work, skip MR`,
	RunE: runDone,
}

var (
	doneIssue    string
	donePriority int
	doneExit     string
)

// Valid exit types for gt done
const (
	ExitCompleted = "COMPLETED"
	ExitEscalated = "ESCALATED"
	ExitDeferred  = "DEFERRED"
)

func init() {
	doneCmd.Flags().StringVar(&doneIssue, "issue", "", "Source issue ID (default: parse from branch name)")
	doneCmd.Flags().IntVarP(&donePriority, "priority", "p", -1, "Override priority (0-4, default: inherit from issue)")
	doneCmd.Flags().StringVar(&doneExit, "exit", ExitCompleted, "Exit type: COMPLETED, ESCALATED, or DEFERRED")

	rootCmd.AddCommand(doneCmd)
}

func runDone(cmd *cobra.Command, args []string) error {
	// Validate exit type
	exitType := strings.ToUpper(doneExit)
	if exitType != ExitCompleted && exitType != ExitEscalated && exitType != ExitDeferred {
		return fmt.Errorf("invalid exit type '%s': must be COMPLETED, ESCALATED, or DEFERRED", doneExit)
	}

	// Find workspace
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Find current rig
	rigName, _, err := findCurrentRig(townRoot)
	if err != nil {
		return err
	}

	// Initialize git for the current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting current directory: %w", err)
	}
	g := git.NewGit(cwd)

	// Get current branch
	branch, err := g.CurrentBranch()
	if err != nil {
		return fmt.Errorf("getting current branch: %w", err)
	}

	// Parse branch info
	info := parseBranchName(branch)

	// Override with explicit flags
	issueID := doneIssue
	if issueID == "" {
		issueID = info.Issue
	}
	worker := info.Worker

	// Determine polecat name from sender detection
	sender := detectSender()
	polecatName := ""
	if parts := strings.Split(sender, "/"); len(parts) >= 2 {
		polecatName = parts[len(parts)-1]
	}

	// For COMPLETED, we need an issue ID and branch must not be main
	var mrID string
	if exitType == ExitCompleted {
		if branch == "main" || branch == "master" {
			return fmt.Errorf("cannot submit main/master branch to merge queue")
		}

		if issueID == "" {
			return fmt.Errorf("cannot determine source issue from branch '%s'; use --issue to specify", branch)
		}

		// Initialize beads
		bd := beads.New(cwd)

		// Determine target branch (auto-detect integration branch if applicable)
		target := "main"
		autoTarget, err := detectIntegrationBranch(bd, g, issueID)
		if err == nil && autoTarget != "" {
			target = autoTarget
		}

		// Get source issue for priority inheritance
		var priority int
		if donePriority >= 0 {
			priority = donePriority
		} else {
			// Try to inherit from source issue
			sourceIssue, err := bd.Show(issueID)
			if err != nil {
				priority = 2 // Default
			} else {
				priority = sourceIssue.Priority
			}
		}

		// Build title
		title := fmt.Sprintf("Merge: %s", issueID)

		// Note: Branch stays local. Refinery sees it via shared .git (worktree).
		// Only main gets pushed to origin after merge.

		// Submit to MR queue (wisp storage - ephemeral, not synced)
		rigPath := filepath.Join(townRoot, rigName)
		queue := mrqueue.New(rigPath)

		mr := &mrqueue.MR{
			Branch:      branch,
			Target:      target,
			SourceIssue: issueID,
			Worker:      worker,
			Rig:         rigName,
			Title:       title,
			Priority:    priority,
		}

		if err := queue.Submit(mr); err != nil {
			return fmt.Errorf("submitting to merge queue: %w", err)
		}
		mrID = mr.ID

		// Success output
		fmt.Printf("%s Work submitted to merge queue\n", style.Bold.Render("✓"))
		fmt.Printf("  MR ID: %s\n", style.Bold.Render(mr.ID))
		fmt.Printf("  Source: %s\n", branch)
		fmt.Printf("  Target: %s\n", target)
		fmt.Printf("  Issue: %s\n", issueID)
		if worker != "" {
			fmt.Printf("  Worker: %s\n", worker)
		}
		fmt.Printf("  Priority: P%d\n", priority)
		fmt.Println()
		fmt.Printf("%s\n", style.Dim.Render("The Refinery will process your merge request."))
	} else {
		// For ESCALATED or DEFERRED, just print status
		fmt.Printf("%s Signaling %s\n", style.Bold.Render("→"), exitType)
		if issueID != "" {
			fmt.Printf("  Issue: %s\n", issueID)
		}
		fmt.Printf("  Branch: %s\n", branch)
	}

	// Notify Witness about completion
	// Use town-level beads for cross-agent mail
	townRouter := mail.NewRouter(townRoot)
	witnessAddr := fmt.Sprintf("%s/witness", rigName)

	// Build notification body
	var bodyLines []string
	bodyLines = append(bodyLines, fmt.Sprintf("Exit: %s", exitType))
	if issueID != "" {
		bodyLines = append(bodyLines, fmt.Sprintf("Issue: %s", issueID))
	}
	if mrID != "" {
		bodyLines = append(bodyLines, fmt.Sprintf("MR: %s", mrID))
	}
	bodyLines = append(bodyLines, fmt.Sprintf("Branch: %s", branch))

	doneNotification := &mail.Message{
		To:      witnessAddr,
		From:    sender,
		Subject: fmt.Sprintf("POLECAT_DONE %s", polecatName),
		Body:    strings.Join(bodyLines, "\n"),
	}

	fmt.Printf("\nNotifying Witness...\n")
	if err := townRouter.Send(doneNotification); err != nil {
		fmt.Printf("  %s\n", style.Dim.Render(fmt.Sprintf("Warning: could not notify witness: %v", err)))
	} else {
		fmt.Printf("%s Witness notified of %s\n", style.Bold.Render("✓"), exitType)
	}

	// Log done event
	LogDone(townRoot, sender, issueID)

	// Update agent bead state (ZFC: self-report completion)
	updateAgentStateOnDone(cwd, townRoot, exitType, issueID)

	return nil
}

// updateAgentStateOnDone updates the agent bead state when work is complete.
// Maps exit type to agent state:
//   - COMPLETED → "done"
//   - ESCALATED → "stuck"
//   - DEFERRED → "idle"
func updateAgentStateOnDone(cwd, townRoot, exitType, issueID string) {
	// Get role context
	roleInfo, err := GetRoleWithContext(cwd, townRoot)
	if err != nil {
		return
	}

	ctx := RoleContext{
		Role:     roleInfo.Role,
		Rig:      roleInfo.Rig,
		Polecat:  roleInfo.Polecat,
		TownRoot: townRoot,
		WorkDir:  cwd,
	}

	agentBeadID := getAgentBeadID(ctx)
	if agentBeadID == "" {
		return
	}

	// Map exit type to agent state
	var newState string
	switch exitType {
	case ExitCompleted:
		newState = "done"
	case ExitEscalated:
		newState = "stuck"
	case ExitDeferred:
		newState = "idle"
	default:
		return
	}

	// Update agent bead with new state and clear hook_bead (work is done)
	bd := beads.New(cwd)
	emptyHook := ""
	if err := bd.UpdateAgentState(agentBeadID, newState, &emptyHook); err != nil {
		// Silently ignore - beads might not be configured
		return
	}
}
