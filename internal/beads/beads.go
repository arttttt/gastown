// Package beads provides a wrapper for the bd (beads) CLI.
package beads

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Common errors
var (
	ErrNotInstalled = errors.New("bd not installed: run 'pip install beads-cli' or see https://github.com/anthropics/beads")
	ErrNotARepo     = errors.New("not a beads repository (no .beads directory found)")
	ErrSyncConflict = errors.New("beads sync conflict")
	ErrNotFound     = errors.New("issue not found")
)

// ResolveBeadsDir returns the actual beads directory, following any redirect.
// If workDir/.beads/redirect exists, it reads the redirect path and resolves it
// relative to workDir (not the .beads directory). Otherwise, returns workDir/.beads.
//
// This is essential for crew workers and polecats that use shared beads via redirect.
// The redirect file contains a relative path like "../../mayor/rig/.beads".
//
// Example: if we're at crew/max/ and .beads/redirect contains "../../mayor/rig/.beads",
// the redirect is resolved from crew/max/ (not crew/max/.beads/), giving us
// mayor/rig/.beads at the rig root level.
//
// Circular redirect detection: If the resolved path equals the original beads directory,
// this indicates an errant redirect file that should be removed. The function logs a
// warning and returns the original beads directory.
func ResolveBeadsDir(workDir string) string {
	beadsDir := filepath.Join(workDir, ".beads")
	redirectPath := filepath.Join(beadsDir, "redirect")

	// Check for redirect file
	data, err := os.ReadFile(redirectPath)
	if err != nil {
		// No redirect, use local .beads
		return beadsDir
	}

	// Read and clean the redirect path
	redirectTarget := strings.TrimSpace(string(data))
	if redirectTarget == "" {
		return beadsDir
	}

	// Resolve relative to workDir (the redirect is written from the perspective
	// of being inside workDir, not inside workDir/.beads)
	// e.g., redirect contains "../../mayor/rig/.beads"
	// from crew/max/, this resolves to mayor/rig/.beads
	resolved := filepath.Join(workDir, redirectTarget)

	// Clean the path to resolve .. components
	resolved = filepath.Clean(resolved)

	// Detect circular redirects: if resolved path equals original beads dir,
	// this is an errant redirect file (e.g., redirect in mayor/rig/.beads pointing to itself)
	if resolved == beadsDir {
		fmt.Fprintf(os.Stderr, "Warning: circular redirect detected in %s (points to itself), ignoring redirect\n", redirectPath)
		// Remove the errant redirect file to prevent future warnings
		if err := os.Remove(redirectPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not remove errant redirect file: %v\n", err)
		}
		return beadsDir
	}

	// Detect redirect chains: check if resolved path also has a redirect
	resolvedRedirect := filepath.Join(resolved, "redirect")
	if _, err := os.Stat(resolvedRedirect); err == nil {
		fmt.Fprintf(os.Stderr, "Warning: redirect chain detected: %s -> %s (which also has a redirect)\n", beadsDir, resolved)
		// Don't follow chains - just return the first resolved path
		// The target's redirect is likely errant and should be removed
	}

	return resolved
}

// Issue represents a beads issue.
type Issue struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Priority    int      `json:"priority"`
	Type        string   `json:"issue_type"`
	CreatedAt   string   `json:"created_at"`
	CreatedBy   string   `json:"created_by,omitempty"`
	UpdatedAt   string   `json:"updated_at"`
	ClosedAt    string   `json:"closed_at,omitempty"`
	Parent      string   `json:"parent,omitempty"`
	Assignee    string   `json:"assignee,omitempty"`
	Children    []string `json:"children,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
	Blocks      []string `json:"blocks,omitempty"`
	BlockedBy   []string `json:"blocked_by,omitempty"`

	// Agent bead slots (type=agent only)
	HookBead string `json:"hook_bead,omitempty"` // Current work attached to agent's hook
	RoleBead string `json:"role_bead,omitempty"` // Role definition bead (shared)

	// Counts from list output
	DependencyCount int `json:"dependency_count,omitempty"`
	DependentCount  int `json:"dependent_count,omitempty"`
	BlockedByCount  int `json:"blocked_by_count,omitempty"`

	// Detailed dependency info from show output
	Dependencies []IssueDep `json:"dependencies,omitempty"`
	Dependents   []IssueDep `json:"dependents,omitempty"`
}

// IssueDep represents a dependency or dependent issue with its relation.
type IssueDep struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	Priority       int    `json:"priority"`
	Type           string `json:"issue_type"`
	DependencyType string `json:"dependency_type,omitempty"`
}

// ListOptions specifies filters for listing issues.
type ListOptions struct {
	Status     string // "open", "closed", "all"
	Type       string // "task", "bug", "feature", "epic"
	Priority   int    // 0-4, -1 for no filter
	Parent     string // filter by parent ID
	Assignee   string // filter by assignee (e.g., "gastown/Toast")
	NoAssignee bool   // filter for issues with no assignee
}

// CreateOptions specifies options for creating an issue.
type CreateOptions struct {
	Title       string
	Type        string // "task", "bug", "feature", "epic"
	Priority    int    // 0-4
	Description string
	Parent      string
	Actor       string // Who is creating this issue (populates created_by)
}

// UpdateOptions specifies options for updating an issue.
type UpdateOptions struct {
	Title        *string
	Status       *string
	Priority     *int
	Description  *string
	Assignee     *string
	AddLabels    []string // Labels to add
	RemoveLabels []string // Labels to remove
	SetLabels    []string // Labels to set (replaces all existing)
}

// SyncStatus represents the sync status of the beads repository.
type SyncStatus struct {
	Branch    string
	Ahead     int
	Behind    int
	Conflicts []string
}

// Beads wraps bd CLI operations for a working directory.
type Beads struct {
	workDir string
}

// New creates a new Beads wrapper for the given directory.
func New(workDir string) *Beads {
	return &Beads{workDir: workDir}
}

// run executes a bd command and returns stdout.
func (b *Beads) run(args ...string) ([]byte, error) {
	cmd := exec.Command("bd", args...)
	cmd.Dir = b.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return nil, b.wrapError(err, stderr.String(), args)
	}

	return stdout.Bytes(), nil
}

// wrapError wraps bd errors with context.
func (b *Beads) wrapError(err error, stderr string, args []string) error {
	stderr = strings.TrimSpace(stderr)

	// Check for bd not installed
	if execErr, ok := err.(*exec.Error); ok && errors.Is(execErr.Err, exec.ErrNotFound) {
		return ErrNotInstalled
	}

	// Detect specific error types from stderr
	if strings.Contains(stderr, "not a beads repository") ||
		strings.Contains(stderr, "No .beads directory") ||
		strings.Contains(stderr, ".beads") && strings.Contains(stderr, "not found") {
		return ErrNotARepo
	}
	if strings.Contains(stderr, "sync conflict") || strings.Contains(stderr, "CONFLICT") {
		return ErrSyncConflict
	}
	if strings.Contains(stderr, "not found") || strings.Contains(stderr, "Issue not found") {
		return ErrNotFound
	}

	if stderr != "" {
		return fmt.Errorf("bd %s: %s", strings.Join(args, " "), stderr)
	}
	return fmt.Errorf("bd %s: %w", strings.Join(args, " "), err)
}

// List returns issues matching the given options.
func (b *Beads) List(opts ListOptions) ([]*Issue, error) {
	args := []string{"list", "--json"}

	if opts.Status != "" {
		args = append(args, "--status="+opts.Status)
	}
	if opts.Type != "" {
		args = append(args, "--type="+opts.Type)
	}
	if opts.Priority >= 0 {
		args = append(args, fmt.Sprintf("--priority=%d", opts.Priority))
	}
	if opts.Parent != "" {
		args = append(args, "--parent="+opts.Parent)
	}
	if opts.Assignee != "" {
		args = append(args, "--assignee="+opts.Assignee)
	}
	if opts.NoAssignee {
		args = append(args, "--no-assignee")
	}

	out, err := b.run(args...)
	if err != nil {
		return nil, err
	}

	var issues []*Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parsing bd list output: %w", err)
	}

	return issues, nil
}

// ListByAssignee returns all issues assigned to a specific assignee.
// The assignee is typically in the format "rig/polecatName" (e.g., "gastown/Toast").
func (b *Beads) ListByAssignee(assignee string) ([]*Issue, error) {
	return b.List(ListOptions{
		Status:   "all", // Include both open and closed for state derivation
		Assignee: assignee,
		Priority: -1, // No priority filter
	})
}

// GetAssignedIssue returns the first open issue assigned to the given assignee.
// Returns nil if no open issue is assigned.
func (b *Beads) GetAssignedIssue(assignee string) (*Issue, error) {
	issues, err := b.List(ListOptions{
		Status:   "open",
		Assignee: assignee,
		Priority: -1,
	})
	if err != nil {
		return nil, err
	}

	// Also check in_progress status explicitly
	if len(issues) == 0 {
		issues, err = b.List(ListOptions{
			Status:   "in_progress",
			Assignee: assignee,
			Priority: -1,
		})
		if err != nil {
			return nil, err
		}
	}

	if len(issues) == 0 {
		return nil, nil
	}

	return issues[0], nil
}

// Ready returns issues that are ready to work (not blocked).
func (b *Beads) Ready() ([]*Issue, error) {
	out, err := b.run("ready", "--json")
	if err != nil {
		return nil, err
	}

	var issues []*Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parsing bd ready output: %w", err)
	}

	return issues, nil
}

// ReadyWithType returns ready issues filtered by type.
// Uses bd ready --type flag for server-side filtering.
func (b *Beads) ReadyWithType(issueType string) ([]*Issue, error) {
	out, err := b.run("ready", "--json", "--type", issueType, "-n", "100")
	if err != nil {
		return nil, err
	}

	var issues []*Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parsing bd ready output: %w", err)
	}

	return issues, nil
}

// Show returns detailed information about an issue.
func (b *Beads) Show(id string) (*Issue, error) {
	out, err := b.run("show", id, "--json")
	if err != nil {
		return nil, err
	}

	// bd show --json returns an array with one element
	var issues []*Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parsing bd show output: %w", err)
	}

	if len(issues) == 0 {
		return nil, ErrNotFound
	}

	return issues[0], nil
}

// Blocked returns issues that are blocked by dependencies.
func (b *Beads) Blocked() ([]*Issue, error) {
	out, err := b.run("blocked", "--json")
	if err != nil {
		return nil, err
	}

	var issues []*Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parsing bd blocked output: %w", err)
	}

	return issues, nil
}

// Create creates a new issue and returns it.
func (b *Beads) Create(opts CreateOptions) (*Issue, error) {
	args := []string{"create", "--json"}

	if opts.Title != "" {
		args = append(args, "--title="+opts.Title)
	}
	if opts.Type != "" {
		args = append(args, "--type="+opts.Type)
	}
	if opts.Priority >= 0 {
		args = append(args, fmt.Sprintf("--priority=%d", opts.Priority))
	}
	if opts.Description != "" {
		args = append(args, "--description="+opts.Description)
	}
	if opts.Parent != "" {
		args = append(args, "--parent="+opts.Parent)
	}
	if opts.Actor != "" {
		args = append(args, "--actor="+opts.Actor)
	}

	out, err := b.run(args...)
	if err != nil {
		return nil, err
	}

	var issue Issue
	if err := json.Unmarshal(out, &issue); err != nil {
		return nil, fmt.Errorf("parsing bd create output: %w", err)
	}

	return &issue, nil
}

// Update updates an existing issue.
func (b *Beads) Update(id string, opts UpdateOptions) error {
	args := []string{"update", id}

	if opts.Title != nil {
		args = append(args, "--title="+*opts.Title)
	}
	if opts.Status != nil {
		args = append(args, "--status="+*opts.Status)
	}
	if opts.Priority != nil {
		args = append(args, fmt.Sprintf("--priority=%d", *opts.Priority))
	}
	if opts.Description != nil {
		args = append(args, "--description="+*opts.Description)
	}
	if opts.Assignee != nil {
		args = append(args, "--assignee="+*opts.Assignee)
	}
	// Label operations: set-labels replaces all, otherwise use add/remove
	if len(opts.SetLabels) > 0 {
		for _, label := range opts.SetLabels {
			args = append(args, "--set-labels="+label)
		}
	} else {
		for _, label := range opts.AddLabels {
			args = append(args, "--add-label="+label)
		}
		for _, label := range opts.RemoveLabels {
			args = append(args, "--remove-label="+label)
		}
	}

	_, err := b.run(args...)
	return err
}

// Close closes one or more issues.
func (b *Beads) Close(ids ...string) error {
	if len(ids) == 0 {
		return nil
	}

	args := append([]string{"close"}, ids...)
	_, err := b.run(args...)
	return err
}

// CloseWithReason closes one or more issues with a reason.
func (b *Beads) CloseWithReason(reason string, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}

	args := append([]string{"close"}, ids...)
	args = append(args, "--reason="+reason)
	_, err := b.run(args...)
	return err
}

// Release moves an in_progress issue back to open status.
// This is used to recover stuck steps when a worker dies mid-task.
// It clears the assignee so the step can be claimed by another worker.
func (b *Beads) Release(id string) error {
	return b.ReleaseWithReason(id, "")
}

// ReleaseWithReason moves an in_progress issue back to open status with a reason.
// The reason is added as a note to the issue for tracking purposes.
func (b *Beads) ReleaseWithReason(id, reason string) error {
	args := []string{"update", id, "--status=open", "--assignee="}

	// Add reason as a note if provided
	if reason != "" {
		args = append(args, "--notes=Released: "+reason)
	}

	_, err := b.run(args...)
	return err
}

// AddDependency adds a dependency: issue depends on dependsOn.
func (b *Beads) AddDependency(issue, dependsOn string) error {
	_, err := b.run("dep", "add", issue, dependsOn)
	return err
}

// RemoveDependency removes a dependency.
func (b *Beads) RemoveDependency(issue, dependsOn string) error {
	_, err := b.run("dep", "remove", issue, dependsOn)
	return err
}

// Sync syncs beads with remote.
func (b *Beads) Sync() error {
	_, err := b.run("sync")
	return err
}

// SyncFromMain syncs beads updates from main branch.
func (b *Beads) SyncFromMain() error {
	_, err := b.run("sync", "--from-main")
	return err
}

// SyncStatus returns the sync status without performing a sync.
func (b *Beads) SyncStatus() (*SyncStatus, error) {
	out, err := b.run("sync", "--status", "--json")
	if err != nil {
		// If sync branch doesn't exist, return empty status
		if strings.Contains(err.Error(), "does not exist") {
			return &SyncStatus{}, nil
		}
		return nil, err
	}

	var status SyncStatus
	if err := json.Unmarshal(out, &status); err != nil {
		return nil, fmt.Errorf("parsing bd sync status output: %w", err)
	}

	return &status, nil
}

// Stats returns repository statistics.
func (b *Beads) Stats() (string, error) {
	out, err := b.run("stats")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// IsBeadsRepo checks if the working directory is a beads repository.
func (b *Beads) IsBeadsRepo() bool {
	_, err := b.run("list", "--limit=1")
	return err == nil || !errors.Is(err, ErrNotARepo)
}

// AgentFields holds structured fields for agent beads.
// These are stored as "key: value" lines in the description.
type AgentFields struct {
	RoleType      string // polecat, witness, refinery, deacon, mayor
	Rig           string // Rig name (empty for global agents like mayor/deacon)
	AgentState    string // spawning, working, done, stuck
	HookBead      string // Currently pinned work bead ID
	RoleBead      string // Role definition bead ID (canonical location; may not exist yet)
	CleanupStatus string // ZFC: polecat self-reports git state (clean, has_uncommitted, has_stash, has_unpushed)
}

// FormatAgentDescription creates a description string from agent fields.
func FormatAgentDescription(title string, fields *AgentFields) string {
	if fields == nil {
		return title
	}

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("role_type: %s", fields.RoleType))

	if fields.Rig != "" {
		lines = append(lines, fmt.Sprintf("rig: %s", fields.Rig))
	} else {
		lines = append(lines, "rig: null")
	}

	lines = append(lines, fmt.Sprintf("agent_state: %s", fields.AgentState))

	if fields.HookBead != "" {
		lines = append(lines, fmt.Sprintf("hook_bead: %s", fields.HookBead))
	} else {
		lines = append(lines, "hook_bead: null")
	}

	if fields.RoleBead != "" {
		lines = append(lines, fmt.Sprintf("role_bead: %s", fields.RoleBead))
	} else {
		lines = append(lines, "role_bead: null")
	}

	if fields.CleanupStatus != "" {
		lines = append(lines, fmt.Sprintf("cleanup_status: %s", fields.CleanupStatus))
	} else {
		lines = append(lines, "cleanup_status: null")
	}

	return strings.Join(lines, "\n")
}

// ParseAgentFields extracts agent fields from an issue's description.
func ParseAgentFields(description string) *AgentFields {
	fields := &AgentFields{}

	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		colonIdx := strings.Index(line, ":")
		if colonIdx == -1 {
			continue
		}

		key := strings.TrimSpace(line[:colonIdx])
		value := strings.TrimSpace(line[colonIdx+1:])
		if value == "null" || value == "" {
			value = ""
		}

		switch strings.ToLower(key) {
		case "role_type":
			fields.RoleType = value
		case "rig":
			fields.Rig = value
		case "agent_state":
			fields.AgentState = value
		case "hook_bead":
			fields.HookBead = value
		case "role_bead":
			fields.RoleBead = value
		case "cleanup_status":
			fields.CleanupStatus = value
		}
	}

	return fields
}

// CreateAgentBead creates an agent bead for tracking agent lifecycle.
// The ID format is: <prefix>-<rig>-<role>-<name> (e.g., gt-gastown-polecat-Toast)
// Use AgentBeadID() helper to generate correct IDs.
func (b *Beads) CreateAgentBead(id, title string, fields *AgentFields) (*Issue, error) {
	description := FormatAgentDescription(title, fields)

	args := []string{"create", "--json",
		"--id=" + id,
		"--type=agent",
		"--title=" + title,
		"--description=" + description,
	}

	out, err := b.run(args...)
	if err != nil {
		return nil, err
	}

	var issue Issue
	if err := json.Unmarshal(out, &issue); err != nil {
		return nil, fmt.Errorf("parsing bd create output: %w", err)
	}

	// Set the role slot if specified (this is the authoritative storage)
	if fields != nil && fields.RoleBead != "" {
		if _, err := b.run("slot", "set", id, "role", fields.RoleBead); err != nil {
			// Non-fatal: warn but continue
			fmt.Printf("Warning: could not set role slot: %v\n", err)
		}
	}

	return &issue, nil
}

// UpdateAgentState updates the agent_state field in an agent bead.
// Optionally updates hook_bead if provided.
//
// IMPORTANT: This function uses the proper bd commands to update agent fields:
// - `bd agent state` for agent_state (uses SQLite column directly)
// - `bd slot set/clear` for hook_bead (uses SQLite column directly)
//
// This ensures consistency with `bd slot show` and other beads commands.
// Previously, this function embedded these fields in the description text,
// which caused inconsistencies with bd slot commands (see GH #gt-9v52).
func (b *Beads) UpdateAgentState(id string, state string, hookBead *string) error {
	// Update agent state using bd agent state command
	// This updates the agent_state column directly in SQLite
	_, err := b.run("agent", "state", id, state)
	if err != nil {
		return fmt.Errorf("updating agent state: %w", err)
	}

	// Update hook_bead if provided
	if hookBead != nil {
		if *hookBead != "" {
			// Set the hook using bd slot set
			// This updates the hook_bead column directly in SQLite
			_, err = b.run("slot", "set", id, "hook", *hookBead)
			if err != nil {
				// If slot is already occupied, clear it first then retry
				// This handles re-slinging scenarios where we're updating the hook
				errStr := err.Error()
				if strings.Contains(errStr, "already occupied") {
					_, _ = b.run("slot", "clear", id, "hook")
					_, err = b.run("slot", "set", id, "hook", *hookBead)
				}
				if err != nil {
					return fmt.Errorf("setting hook: %w", err)
				}
			}
		} else {
			// Clear the hook
			_, err = b.run("slot", "clear", id, "hook")
			if err != nil {
				return fmt.Errorf("clearing hook: %w", err)
			}
		}
	}

	return nil
}

// UpdateAgentCleanupStatus updates the cleanup_status field in an agent bead.
// This is called by the polecat to self-report its git state (ZFC compliance).
// Valid statuses: clean, has_uncommitted, has_stash, has_unpushed
func (b *Beads) UpdateAgentCleanupStatus(id string, cleanupStatus string) error {
	// First get current issue to preserve other fields
	issue, err := b.Show(id)
	if err != nil {
		return err
	}

	// Parse existing fields
	fields := ParseAgentFields(issue.Description)
	fields.CleanupStatus = cleanupStatus

	// Format new description
	description := FormatAgentDescription(issue.Title, fields)

	return b.Update(id, UpdateOptions{Description: &description})
}

// DeleteAgentBead permanently deletes an agent bead.
// Uses --hard --force for immediate permanent deletion (no tombstone).
func (b *Beads) DeleteAgentBead(id string) error {
	_, err := b.run("delete", id, "--hard", "--force")
	return err
}

// GetAgentBead retrieves an agent bead by ID.
// Returns nil if not found.
func (b *Beads) GetAgentBead(id string) (*Issue, *AgentFields, error) {
	issue, err := b.Show(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}

	if issue.Type != "agent" {
		return nil, nil, fmt.Errorf("issue %s is not an agent bead (type: %s)", id, issue.Type)
	}

	fields := ParseAgentFields(issue.Description)
	return issue, fields, nil
}

// Agent bead ID naming convention:
//   prefix-rig-role-name
//
// Examples:
//   - gt-mayor (town-level, no rig)
//   - gt-deacon (town-level, no rig)
//   - gt-gastown-witness (rig-level singleton)
//   - gt-gastown-refinery (rig-level singleton)
//   - gt-gastown-crew-max (rig-level named agent)
//   - gt-gastown-polecat-Toast (rig-level named agent)

// AgentBeadIDWithPrefix generates an agent bead ID using the specified prefix.
// The prefix should NOT include the hyphen (e.g., "gt", "bd", not "gt-", "bd-").
// For town-level agents (mayor, deacon), pass empty rig and name.
// For rig-level singletons (witness, refinery), pass empty name.
// For named agents (crew, polecat), pass all three.
func AgentBeadIDWithPrefix(prefix, rig, role, name string) string {
	if rig == "" {
		// Town-level agent: prefix-mayor, prefix-deacon
		return prefix + "-" + role
	}
	if name == "" {
		// Rig-level singleton: prefix-rig-witness, prefix-rig-refinery
		return prefix + "-" + rig + "-" + role
	}
	// Rig-level named agent: prefix-rig-role-name
	return prefix + "-" + rig + "-" + role + "-" + name
}

// AgentBeadID generates the canonical agent bead ID using "gt" prefix.
// For non-gastown rigs, use AgentBeadIDWithPrefix with the rig's configured prefix.
func AgentBeadID(rig, role, name string) string {
	return AgentBeadIDWithPrefix("gt", rig, role, name)
}

// MayorBeadID returns the Mayor agent bead ID.
func MayorBeadID() string {
	return "gt-mayor"
}

// DeaconBeadID returns the Deacon agent bead ID.
func DeaconBeadID() string {
	return "gt-deacon"
}

// WitnessBeadIDWithPrefix returns the Witness agent bead ID for a rig using the specified prefix.
func WitnessBeadIDWithPrefix(prefix, rig string) string {
	return AgentBeadIDWithPrefix(prefix, rig, "witness", "")
}

// WitnessBeadID returns the Witness agent bead ID for a rig using "gt" prefix.
func WitnessBeadID(rig string) string {
	return WitnessBeadIDWithPrefix("gt", rig)
}

// RefineryBeadIDWithPrefix returns the Refinery agent bead ID for a rig using the specified prefix.
func RefineryBeadIDWithPrefix(prefix, rig string) string {
	return AgentBeadIDWithPrefix(prefix, rig, "refinery", "")
}

// RefineryBeadID returns the Refinery agent bead ID for a rig using "gt" prefix.
func RefineryBeadID(rig string) string {
	return RefineryBeadIDWithPrefix("gt", rig)
}

// CrewBeadIDWithPrefix returns a Crew worker agent bead ID using the specified prefix.
func CrewBeadIDWithPrefix(prefix, rig, name string) string {
	return AgentBeadIDWithPrefix(prefix, rig, "crew", name)
}

// CrewBeadID returns a Crew worker agent bead ID using "gt" prefix.
func CrewBeadID(rig, name string) string {
	return CrewBeadIDWithPrefix("gt", rig, name)
}

// PolecatBeadIDWithPrefix returns a Polecat agent bead ID using the specified prefix.
func PolecatBeadIDWithPrefix(prefix, rig, name string) string {
	return AgentBeadIDWithPrefix(prefix, rig, "polecat", name)
}

// PolecatBeadID returns a Polecat agent bead ID using "gt" prefix.
func PolecatBeadID(rig, name string) string {
	return PolecatBeadIDWithPrefix("gt", rig, name)
}

// ParseAgentBeadID parses an agent bead ID into its components.
// Returns rig, role, name, and whether parsing succeeded.
// For town-level agents, rig will be empty.
// For singletons, name will be empty.
func ParseAgentBeadID(id string) (rig, role, name string, ok bool) {
	if !strings.HasPrefix(id, "gt-") {
		return "", "", "", false
	}
	rest := strings.TrimPrefix(id, "gt-")
	parts := strings.Split(rest, "-")

	switch len(parts) {
	case 1:
		// Town-level: gt-mayor, gt-deacon
		return "", parts[0], "", true
	case 2:
		// Rig-level singleton: gt-gastown-witness
		return parts[0], parts[1], "", true
	case 3:
		// Rig-level named: gt-gastown-crew-max
		return parts[0], parts[1], parts[2], true
	default:
		// Handle names with hyphens: gt-gastown-polecat-my-agent-name
		if len(parts) >= 3 {
			return parts[0], parts[1], strings.Join(parts[2:], "-"), true
		}
		return "", "", "", false
	}
}

// IsAgentSessionBead returns true if the bead ID represents an agent session molecule.
// Agent session beads follow patterns like gt-mayor, gt-gastown-witness, gt-gastown-crew-joe.
// These are used to track agent state and update frequently, which can create noise.
func IsAgentSessionBead(beadID string) bool {
	_, role, _, ok := ParseAgentBeadID(beadID)
	if !ok {
		return false
	}
	// Known agent roles
	switch role {
	case "mayor", "deacon", "witness", "refinery", "crew", "polecat":
		return true
	default:
		return false
	}
}

// Role bead ID naming convention:
//   gt-<role>-role
//
// Examples:
//   - gt-mayor-role
//   - gt-deacon-role
//   - gt-witness-role
//   - gt-refinery-role
//   - gt-crew-role
//   - gt-polecat-role

// RoleBeadID returns the role bead ID for a given role type.
// Role beads define lifecycle configuration for each agent type.
func RoleBeadID(roleType string) string {
	return "gt-" + roleType + "-role"
}

// MayorRoleBeadID returns the Mayor role bead ID.
func MayorRoleBeadID() string {
	return RoleBeadID("mayor")
}

// DeaconRoleBeadID returns the Deacon role bead ID.
func DeaconRoleBeadID() string {
	return RoleBeadID("deacon")
}

// WitnessRoleBeadID returns the Witness role bead ID.
func WitnessRoleBeadID() string {
	return RoleBeadID("witness")
}

// RefineryRoleBeadID returns the Refinery role bead ID.
func RefineryRoleBeadID() string {
	return RoleBeadID("refinery")
}

// CrewRoleBeadID returns the Crew role bead ID.
func CrewRoleBeadID() string {
	return RoleBeadID("crew")
}

// PolecatRoleBeadID returns the Polecat role bead ID.
func PolecatRoleBeadID() string {
	return RoleBeadID("polecat")
}

// GetRoleConfig looks up a role bead and returns its parsed RoleConfig.
// Returns nil, nil if the role bead doesn't exist or has no config.
func (b *Beads) GetRoleConfig(roleBeadID string) (*RoleConfig, error) {
	issue, err := b.Show(roleBeadID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}

	if issue.Type != "role" {
		return nil, fmt.Errorf("bead %s is not a role bead (type: %s)", roleBeadID, issue.Type)
	}

	return ParseRoleConfig(issue.Description), nil
}
