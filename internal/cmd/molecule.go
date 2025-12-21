package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

// Molecule command flags
var (
	moleculeJSON          bool
	moleculeInstParent    string
	moleculeInstContext   []string
	moleculeCatalogOnly   bool // List only catalog templates
	moleculeDBOnly        bool // List only database molecules
)

var moleculeCmd = &cobra.Command{
	Use:   "molecule",
	Short: "Molecule workflow commands",
	Long: `Manage molecule workflow templates.

Molecules are composable workflow patterns stored as beads issues.
When instantiated on a parent issue, they create child beads forming a DAG.`,
}

var moleculeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List molecules",
	Long: `List all molecule definitions.

By default, lists molecules from all sources:
- Built-in molecules (shipped with gt)
- Town-level: <town>/.beads/molecules.jsonl
- Rig-level: <rig>/.beads/molecules.jsonl
- Project-level: .beads/molecules.jsonl
- Database: molecules stored as issues

Use --catalog to show only template molecules (not instantiated).
Use --db to show only database molecules.`,
	RunE: runMoleculeList,
}

var moleculeExportCmd = &cobra.Command{
	Use:   "export <path>",
	Short: "Export built-in molecules to JSONL",
	Long: `Export built-in molecule templates to a JSONL file.

This creates a molecules.jsonl file containing all built-in molecules.
You can place this in:
- <town>/.beads/molecules.jsonl (town-level)
- <rig>/.beads/molecules.jsonl (rig-level)
- .beads/molecules.jsonl (project-level)

The file can be edited to customize or add new molecules.`,
	Args: cobra.ExactArgs(1),
	RunE: runMoleculeExport,
}

var moleculeShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show molecule with parsed steps",
	Long: `Show a molecule definition with its parsed steps.

Displays the molecule's title, description structure, and all defined steps
with their dependencies.`,
	Args: cobra.ExactArgs(1),
	RunE: runMoleculeShow,
}

var moleculeParseCmd = &cobra.Command{
	Use:   "parse <id>",
	Short: "Validate and show parsed structure",
	Long: `Parse and validate a molecule definition.

This command parses the molecule's step definitions and reports any errors.
Useful for debugging molecule definitions before instantiation.`,
	Args: cobra.ExactArgs(1),
	RunE: runMoleculeParse,
}

var moleculeInstantiateCmd = &cobra.Command{
	Use:   "instantiate <mol-id>",
	Short: "Create steps from molecule template",
	Long: `Instantiate a molecule on a parent issue.

Creates child issues for each step defined in the molecule, wiring up
dependencies according to the Needs: declarations.

Template variables ({{variable}}) can be substituted using --context flags.

Examples:
  gt molecule instantiate mol-xyz --parent=gt-abc
  gt molecule instantiate mol-xyz --parent=gt-abc --context feature=auth --context file=login.go`,
	Args: cobra.ExactArgs(1),
	RunE: runMoleculeInstantiate,
}

var moleculeInstancesCmd = &cobra.Command{
	Use:   "instances <mol-id>",
	Short: "Show all instantiations of a molecule",
	Long: `Show all parent issues that have instantiated this molecule.

Lists each instantiation with its status and progress.`,
	Args: cobra.ExactArgs(1),
	RunE: runMoleculeInstances,
}

var moleculeProgressCmd = &cobra.Command{
	Use:   "progress <root-issue-id>",
	Short: "Show progress through a molecule's steps",
	Long: `Show the execution progress of an instantiated molecule.

Given a root issue (the parent of molecule steps), displays:
- Total steps and completion status
- Which steps are done, in-progress, ready, or blocked
- Overall progress percentage

This is useful for the Witness to monitor molecule execution.

Example:
  gt molecule progress gt-abc`,
	Args: cobra.ExactArgs(1),
	RunE: runMoleculeProgress,
}

var moleculeAttachCmd = &cobra.Command{
	Use:   "attach <pinned-bead-id> <molecule-id>",
	Short: "Attach a molecule to a pinned bead",
	Long: `Attach a molecule to a pinned/handoff bead.

This records which molecule an agent is currently working on. The attachment
is stored in the pinned bead's description and visible via 'bd show'.

Example:
  gt molecule attach gt-abc mol-xyz`,
	Args: cobra.ExactArgs(2),
	RunE: runMoleculeAttach,
}

var moleculeDetachCmd = &cobra.Command{
	Use:   "detach <pinned-bead-id>",
	Short: "Detach molecule from a pinned bead",
	Long: `Remove molecule attachment from a pinned/handoff bead.

This clears the attached_molecule and attached_at fields from the bead.

Example:
  gt molecule detach gt-abc`,
	Args: cobra.ExactArgs(1),
	RunE: runMoleculeDetach,
}

var moleculeAttachmentCmd = &cobra.Command{
	Use:   "attachment <pinned-bead-id>",
	Short: "Show attachment status of a pinned bead",
	Long: `Show which molecule is attached to a pinned bead.

Example:
  gt molecule attachment gt-abc`,
	Args: cobra.ExactArgs(1),
	RunE: runMoleculeAttachment,
}

func init() {
	// List flags
	moleculeListCmd.Flags().BoolVar(&moleculeJSON, "json", false, "Output as JSON")
	moleculeListCmd.Flags().BoolVar(&moleculeCatalogOnly, "catalog", false, "Show only catalog templates")
	moleculeListCmd.Flags().BoolVar(&moleculeDBOnly, "db", false, "Show only database molecules")

	// Show flags
	moleculeShowCmd.Flags().BoolVar(&moleculeJSON, "json", false, "Output as JSON")

	// Parse flags
	moleculeParseCmd.Flags().BoolVar(&moleculeJSON, "json", false, "Output as JSON")

	// Instantiate flags
	moleculeInstantiateCmd.Flags().StringVar(&moleculeInstParent, "parent", "", "Parent issue ID (required)")
	moleculeInstantiateCmd.Flags().StringArrayVar(&moleculeInstContext, "context", nil, "Context variable (key=value)")
	moleculeInstantiateCmd.MarkFlagRequired("parent")

	// Instances flags
	moleculeInstancesCmd.Flags().BoolVar(&moleculeJSON, "json", false, "Output as JSON")

	// Progress flags
	moleculeProgressCmd.Flags().BoolVar(&moleculeJSON, "json", false, "Output as JSON")

	// Attachment flags
	moleculeAttachmentCmd.Flags().BoolVar(&moleculeJSON, "json", false, "Output as JSON")

	// Add subcommands
	moleculeCmd.AddCommand(moleculeListCmd)
	moleculeCmd.AddCommand(moleculeShowCmd)
	moleculeCmd.AddCommand(moleculeParseCmd)
	moleculeCmd.AddCommand(moleculeInstantiateCmd)
	moleculeCmd.AddCommand(moleculeInstancesCmd)
	moleculeCmd.AddCommand(moleculeExportCmd)
	moleculeCmd.AddCommand(moleculeProgressCmd)
	moleculeCmd.AddCommand(moleculeAttachCmd)
	moleculeCmd.AddCommand(moleculeDetachCmd)
	moleculeCmd.AddCommand(moleculeAttachmentCmd)

	rootCmd.AddCommand(moleculeCmd)
}

func runMoleculeList(cmd *cobra.Command, args []string) error {
	workDir, err := findLocalBeadsDir()
	if err != nil {
		return fmt.Errorf("not in a beads workspace: %w", err)
	}

	// Collect molecules from requested sources
	type moleculeEntry struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Source      string `json:"source"`
		StepCount   int    `json:"step_count,omitempty"`
		Status      string `json:"status,omitempty"`
		Description string `json:"description,omitempty"`
	}

	var entries []moleculeEntry

	// Load from catalog (unless --db only)
	if !moleculeDBOnly {
		catalog, err := loadMoleculeCatalog(workDir)
		if err != nil {
			return fmt.Errorf("loading catalog: %w", err)
		}

		for _, mol := range catalog.List() {
			steps, _ := beads.ParseMoleculeSteps(mol.Description)
			entries = append(entries, moleculeEntry{
				ID:          mol.ID,
				Title:       mol.Title,
				Source:      mol.Source,
				StepCount:   len(steps),
				Description: mol.Description,
			})
		}
	}

	// Load from database (unless --catalog only)
	if !moleculeCatalogOnly {
		b := beads.New(workDir)
		issues, err := b.List(beads.ListOptions{
			Type:     "molecule",
			Status:   "all",
			Priority: -1,
		})
		if err != nil {
			return fmt.Errorf("listing molecules: %w", err)
		}

		// Track catalog IDs to avoid duplicates
		catalogIDs := make(map[string]bool)
		for _, e := range entries {
			catalogIDs[e.ID] = true
		}

		for _, mol := range issues {
			// Skip if already in catalog (catalog takes precedence)
			if catalogIDs[mol.ID] {
				continue
			}

			steps, _ := beads.ParseMoleculeSteps(mol.Description)
			entries = append(entries, moleculeEntry{
				ID:          mol.ID,
				Title:       mol.Title,
				Source:      "database",
				StepCount:   len(steps),
				Status:      mol.Status,
				Description: mol.Description,
			})
		}
	}

	if moleculeJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}

	// Human-readable output
	fmt.Printf("%s Molecules (%d)\n\n", style.Bold.Render("🧬"), len(entries))

	if len(entries) == 0 {
		fmt.Printf("  %s\n", style.Dim.Render("(no molecules defined)"))
		return nil
	}

	for _, mol := range entries {
		sourceMarker := style.Dim.Render(fmt.Sprintf("[%s]", mol.Source))

		stepCount := ""
		if mol.StepCount > 0 {
			stepCount = fmt.Sprintf(" (%d steps)", mol.StepCount)
		}

		statusMarker := ""
		if mol.Status == "closed" {
			statusMarker = " " + style.Dim.Render("[closed]")
		}

		fmt.Printf("  %s: %s%s%s %s\n",
			style.Bold.Render(mol.ID), mol.Title, stepCount, statusMarker, sourceMarker)
	}

	return nil
}

// loadMoleculeCatalog loads the molecule catalog with hierarchical sources.
func loadMoleculeCatalog(workDir string) (*beads.MoleculeCatalog, error) {
	var townRoot, rigPath, projectPath string

	// Try to find town root
	townRoot, _ = workspace.FindFromCwd()

	// Try to find rig path
	if townRoot != "" {
		rigName, _, err := findCurrentRig(townRoot)
		if err == nil && rigName != "" {
			rigPath = filepath.Join(townRoot, rigName)
		}
	}

	// Project path is the work directory
	projectPath = workDir

	return beads.LoadCatalog(townRoot, rigPath, projectPath)
}

func runMoleculeExport(cmd *cobra.Command, args []string) error {
	path := args[0]

	if err := beads.ExportBuiltinMolecules(path); err != nil {
		return fmt.Errorf("exporting molecules: %w", err)
	}

	fmt.Printf("%s Exported %d built-in molecules to %s\n",
		style.Bold.Render("✓"), len(beads.BuiltinMolecules()), path)

	return nil
}

func runMoleculeShow(cmd *cobra.Command, args []string) error {
	molID := args[0]

	workDir, err := findLocalBeadsDir()
	if err != nil {
		return fmt.Errorf("not in a beads workspace: %w", err)
	}

	// Try catalog first
	catalog, err := loadMoleculeCatalog(workDir)
	if err != nil {
		return fmt.Errorf("loading catalog: %w", err)
	}

	var mol *beads.Issue
	var source string

	if catalogMol := catalog.Get(molID); catalogMol != nil {
		mol = catalogMol.ToIssue()
		source = catalogMol.Source
	} else {
		// Fall back to database
		b := beads.New(workDir)
		mol, err = b.Show(molID)
		if err != nil {
			return fmt.Errorf("getting molecule: %w", err)
		}
		source = "database"
	}

	if mol.Type != "molecule" {
		return fmt.Errorf("%s is not a molecule (type: %s)", molID, mol.Type)
	}

	// Parse steps
	steps, parseErr := beads.ParseMoleculeSteps(mol.Description)
	_ = source // Used below in output

	// For JSON, include parsed steps
	if moleculeJSON {
		type moleculeOutput struct {
			*beads.Issue
			Source     string               `json:"source"`
			Steps      []beads.MoleculeStep `json:"steps,omitempty"`
			ParseError string               `json:"parse_error,omitempty"`
		}
		out := moleculeOutput{Issue: mol, Source: source, Steps: steps}
		if parseErr != nil {
			out.ParseError = parseErr.Error()
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	// Human-readable output
	fmt.Printf("\n%s: %s %s\n", style.Bold.Render(mol.ID), mol.Title, style.Dim.Render(fmt.Sprintf("[%s]", source)))
	fmt.Printf("Type: %s\n", mol.Type)

	if parseErr != nil {
		fmt.Printf("\n%s Parse error: %s\n", style.Bold.Render("⚠"), parseErr)
	}

	// Show steps
	fmt.Printf("\nSteps (%d):\n", len(steps))
	if len(steps) == 0 {
		fmt.Printf("  %s\n", style.Dim.Render("(no steps defined)"))
	} else {
		// Find which steps are ready (no dependencies)
		for _, step := range steps {
			needsStr := ""
			if len(step.Needs) == 0 {
				needsStr = style.Dim.Render("(ready first)")
			} else {
				needsStr = fmt.Sprintf("Needs: %s", strings.Join(step.Needs, ", "))
			}

			tierStr := ""
			if step.Tier != "" {
				tierStr = fmt.Sprintf(" [%s]", step.Tier)
			}

			fmt.Printf("  %-12s → %s%s\n", step.Ref, needsStr, tierStr)
		}
	}

	// Count instances (need beads client for this)
	b := beads.New(workDir)
	instances, _ := findMoleculeInstances(b, molID)
	fmt.Printf("\nInstances: %d\n", len(instances))

	return nil
}

func runMoleculeParse(cmd *cobra.Command, args []string) error {
	molID := args[0]

	workDir, err := findLocalBeadsDir()
	if err != nil {
		return fmt.Errorf("not in a beads workspace: %w", err)
	}

	b := beads.New(workDir)
	mol, err := b.Show(molID)
	if err != nil {
		return fmt.Errorf("getting molecule: %w", err)
	}

	// Validate the molecule
	validationErr := beads.ValidateMolecule(mol)

	// Parse steps regardless of validation
	steps, parseErr := beads.ParseMoleculeSteps(mol.Description)

	if moleculeJSON {
		type parseOutput struct {
			Valid           bool                 `json:"valid"`
			ValidationError string               `json:"validation_error,omitempty"`
			ParseError      string               `json:"parse_error,omitempty"`
			Steps           []beads.MoleculeStep `json:"steps"`
		}
		out := parseOutput{
			Valid: validationErr == nil,
			Steps: steps,
		}
		if validationErr != nil {
			out.ValidationError = validationErr.Error()
		}
		if parseErr != nil {
			out.ParseError = parseErr.Error()
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	// Human-readable output
	fmt.Printf("\n%s: %s\n\n", style.Bold.Render(mol.ID), mol.Title)

	if validationErr != nil {
		fmt.Printf("%s Validation failed: %s\n\n", style.Bold.Render("✗"), validationErr)
	} else {
		fmt.Printf("%s Valid molecule\n\n", style.Bold.Render("✓"))
	}

	if parseErr != nil {
		fmt.Printf("Parse error: %s\n\n", parseErr)
	}

	fmt.Printf("Parsed Steps (%d):\n", len(steps))
	for i, step := range steps {
		fmt.Printf("\n  [%d] %s\n", i+1, style.Bold.Render(step.Ref))
		if step.Title != step.Ref {
			fmt.Printf("      Title: %s\n", step.Title)
		}
		if len(step.Needs) > 0 {
			fmt.Printf("      Needs: %s\n", strings.Join(step.Needs, ", "))
		}
		if step.Tier != "" {
			fmt.Printf("      Tier: %s\n", step.Tier)
		}
		if step.Instructions != "" {
			// Show first line of instructions
			firstLine := strings.SplitN(step.Instructions, "\n", 2)[0]
			if len(firstLine) > 60 {
				firstLine = firstLine[:57] + "..."
			}
			fmt.Printf("      Instructions: %s\n", style.Dim.Render(firstLine))
		}
	}

	return nil
}

func runMoleculeInstantiate(cmd *cobra.Command, args []string) error {
	molID := args[0]

	workDir, err := findLocalBeadsDir()
	if err != nil {
		return fmt.Errorf("not in a beads workspace: %w", err)
	}

	b := beads.New(workDir)

	// Try catalog first
	catalog, err := loadMoleculeCatalog(workDir)
	if err != nil {
		return fmt.Errorf("loading catalog: %w", err)
	}

	var mol *beads.Issue

	if catalogMol := catalog.Get(molID); catalogMol != nil {
		mol = catalogMol.ToIssue()
	} else {
		// Fall back to database
		mol, err = b.Show(molID)
		if err != nil {
			return fmt.Errorf("getting molecule: %w", err)
		}
	}

	if mol.Type != "molecule" {
		return fmt.Errorf("%s is not a molecule (type: %s)", molID, mol.Type)
	}

	// Validate molecule
	if err := beads.ValidateMolecule(mol); err != nil {
		return fmt.Errorf("invalid molecule: %w", err)
	}

	// Get the parent issue
	parent, err := b.Show(moleculeInstParent)
	if err != nil {
		return fmt.Errorf("getting parent issue: %w", err)
	}

	// Parse context variables
	ctx := make(map[string]string)
	for _, kv := range moleculeInstContext {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid context format %q (expected key=value)", kv)
		}
		ctx[parts[0]] = parts[1]
	}

	// Instantiate the molecule
	opts := beads.InstantiateOptions{Context: ctx}
	steps, err := b.InstantiateMolecule(mol, parent, opts)
	if err != nil {
		return fmt.Errorf("instantiating molecule: %w", err)
	}

	fmt.Printf("%s Created %d steps from %s on %s\n\n",
		style.Bold.Render("✓"), len(steps), molID, moleculeInstParent)

	for _, step := range steps {
		fmt.Printf("  %s: %s\n", style.Dim.Render(step.ID), step.Title)
	}

	return nil
}

func runMoleculeInstances(cmd *cobra.Command, args []string) error {
	molID := args[0]

	workDir, err := findLocalBeadsDir()
	if err != nil {
		return fmt.Errorf("not in a beads workspace: %w", err)
	}

	b := beads.New(workDir)

	// Verify the molecule exists
	mol, err := b.Show(molID)
	if err != nil {
		return fmt.Errorf("getting molecule: %w", err)
	}

	if mol.Type != "molecule" {
		return fmt.Errorf("%s is not a molecule (type: %s)", molID, mol.Type)
	}

	// Find all instances
	instances, err := findMoleculeInstances(b, molID)
	if err != nil {
		return fmt.Errorf("finding instances: %w", err)
	}

	if moleculeJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(instances)
	}

	// Human-readable output
	fmt.Printf("\n%s Instances of %s (%d)\n\n",
		style.Bold.Render("📋"), molID, len(instances))

	if len(instances) == 0 {
		fmt.Printf("  %s\n", style.Dim.Render("(no instantiations found)"))
		return nil
	}

	fmt.Printf("%-16s %-12s %s\n",
		style.Bold.Render("Parent"),
		style.Bold.Render("Status"),
		style.Bold.Render("Created"))
	fmt.Println(strings.Repeat("-", 50))

	for _, inst := range instances {
		// Calculate progress from children
		progress := ""
		if len(inst.Children) > 0 {
			closed := 0
			for _, childID := range inst.Children {
				child, err := b.Show(childID)
				if err == nil && child.Status == "closed" {
					closed++
				}
			}
			progress = fmt.Sprintf(" (%d/%d complete)", closed, len(inst.Children))
		}

		statusStr := inst.Status
		if inst.Status == "closed" {
			statusStr = style.Dim.Render("done")
		} else if inst.Status == "in_progress" {
			statusStr = "active"
		}

		created := ""
		if inst.CreatedAt != "" {
			// Parse and format date
			created = inst.CreatedAt[:10] // Just the date portion
		}

		fmt.Printf("%-16s %-12s %s%s\n", inst.ID, statusStr, created, progress)
	}

	return nil
}

// moleculeInstance represents an instantiation of a molecule.
type moleculeInstance struct {
	*beads.Issue
}

// findMoleculeInstances finds all parent issues that have steps instantiated from the given molecule.
func findMoleculeInstances(b *beads.Beads, molID string) ([]*beads.Issue, error) {
	// Get all issues and look for ones with children that have instantiated_from metadata
	// This is a brute-force approach - could be optimized with better queries

	// Strategy: search for issues whose descriptions contain "instantiated_from: <molID>"
	allIssues, err := b.List(beads.ListOptions{Status: "all", Priority: -1})
	if err != nil {
		return nil, err
	}

	// Find issues that reference this molecule
	parentIDs := make(map[string]bool)
	for _, issue := range allIssues {
		if strings.Contains(issue.Description, fmt.Sprintf("instantiated_from: %s", molID)) {
			// This is a step - find its parent
			if issue.Parent != "" {
				parentIDs[issue.Parent] = true
			}
		}
	}

	// Fetch the parent issues
	var parents []*beads.Issue
	for parentID := range parentIDs {
		parent, err := b.Show(parentID)
		if err == nil {
			parents = append(parents, parent)
		}
	}

	return parents, nil
}

// MoleculeProgressInfo contains progress information for a molecule instance.
type MoleculeProgressInfo struct {
	RootID       string   `json:"root_id"`
	RootTitle    string   `json:"root_title"`
	MoleculeID   string   `json:"molecule_id,omitempty"`
	TotalSteps   int      `json:"total_steps"`
	DoneSteps    int      `json:"done_steps"`
	InProgress   int      `json:"in_progress_steps"`
	ReadySteps   []string `json:"ready_steps"`
	BlockedSteps []string `json:"blocked_steps"`
	Percent      int      `json:"percent_complete"`
	Complete     bool     `json:"complete"`
}

func runMoleculeProgress(cmd *cobra.Command, args []string) error {
	rootID := args[0]

	workDir, err := findLocalBeadsDir()
	if err != nil {
		return fmt.Errorf("not in a beads workspace: %w", err)
	}

	b := beads.New(workDir)

	// Get the root issue
	root, err := b.Show(rootID)
	if err != nil {
		return fmt.Errorf("getting root issue: %w", err)
	}

	// Find all children of the root issue
	children, err := b.List(beads.ListOptions{
		Parent:   rootID,
		Status:   "all",
		Priority: -1,
	})
	if err != nil {
		return fmt.Errorf("listing children: %w", err)
	}

	if len(children) == 0 {
		return fmt.Errorf("no steps found for %s (not a molecule root?)", rootID)
	}

	// Build progress info
	progress := MoleculeProgressInfo{
		RootID:    rootID,
		RootTitle: root.Title,
	}

	// Try to find molecule ID from first child's description
	for _, child := range children {
		if molID := extractMoleculeID(child.Description); molID != "" {
			progress.MoleculeID = molID
			break
		}
	}

	// Build set of closed issue IDs for dependency checking
	closedIDs := make(map[string]bool)
	for _, child := range children {
		if child.Status == "closed" {
			closedIDs[child.ID] = true
		}
	}

	// Categorize steps
	for _, child := range children {
		progress.TotalSteps++

		switch child.Status {
		case "closed":
			progress.DoneSteps++
		case "in_progress":
			progress.InProgress++
		case "open":
			// Check if all dependencies are closed
			allDepsClosed := true
			for _, depID := range child.DependsOn {
				if !closedIDs[depID] {
					allDepsClosed = false
					break
				}
			}

			if len(child.DependsOn) == 0 || allDepsClosed {
				progress.ReadySteps = append(progress.ReadySteps, child.ID)
			} else {
				progress.BlockedSteps = append(progress.BlockedSteps, child.ID)
			}
		}
	}

	// Calculate completion percentage
	if progress.TotalSteps > 0 {
		progress.Percent = (progress.DoneSteps * 100) / progress.TotalSteps
	}
	progress.Complete = progress.DoneSteps == progress.TotalSteps

	// JSON output
	if moleculeJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(progress)
	}

	// Human-readable output
	fmt.Printf("\n%s %s\n\n", style.Bold.Render("🧬 Molecule Progress:"), root.Title)
	fmt.Printf("  Root: %s\n", rootID)
	if progress.MoleculeID != "" {
		fmt.Printf("  Molecule: %s\n", progress.MoleculeID)
	}
	fmt.Println()

	// Progress bar
	barWidth := 20
	filled := (progress.Percent * barWidth) / 100
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
	fmt.Printf("  [%s] %d%% (%d/%d)\n\n", bar, progress.Percent, progress.DoneSteps, progress.TotalSteps)

	// Step status
	fmt.Printf("  Done:        %d\n", progress.DoneSteps)
	fmt.Printf("  In Progress: %d\n", progress.InProgress)
	fmt.Printf("  Ready:       %d", len(progress.ReadySteps))
	if len(progress.ReadySteps) > 0 {
		fmt.Printf(" (%s)", strings.Join(progress.ReadySteps, ", "))
	}
	fmt.Println()
	fmt.Printf("  Blocked:     %d\n", len(progress.BlockedSteps))

	if progress.Complete {
		fmt.Printf("\n  %s\n", style.Bold.Render("✓ Molecule complete!"))
	}

	return nil
}

// extractMoleculeID extracts the molecule ID from an issue's description.
func extractMoleculeID(description string) string {
	lines := strings.Split(description, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "instantiated_from:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "instantiated_from:"))
		}
	}
	return ""
}

func runMoleculeAttach(cmd *cobra.Command, args []string) error {
	pinnedBeadID := args[0]
	moleculeID := args[1]

	workDir, err := findLocalBeadsDir()
	if err != nil {
		return fmt.Errorf("not in a beads workspace: %w", err)
	}

	b := beads.New(workDir)

	// Attach the molecule
	issue, err := b.AttachMolecule(pinnedBeadID, moleculeID)
	if err != nil {
		return fmt.Errorf("attaching molecule: %w", err)
	}

	attachment := beads.ParseAttachmentFields(issue)
	fmt.Printf("%s Attached %s to %s\n", style.Bold.Render("✓"), moleculeID, pinnedBeadID)
	if attachment != nil && attachment.AttachedAt != "" {
		fmt.Printf("  attached_at: %s\n", attachment.AttachedAt)
	}

	return nil
}

func runMoleculeDetach(cmd *cobra.Command, args []string) error {
	pinnedBeadID := args[0]

	workDir, err := findLocalBeadsDir()
	if err != nil {
		return fmt.Errorf("not in a beads workspace: %w", err)
	}

	b := beads.New(workDir)

	// Check current attachment first
	attachment, err := b.GetAttachment(pinnedBeadID)
	if err != nil {
		return fmt.Errorf("checking attachment: %w", err)
	}

	if attachment == nil {
		fmt.Printf("%s No molecule attached to %s\n", style.Dim.Render("ℹ"), pinnedBeadID)
		return nil
	}

	previousMolecule := attachment.AttachedMolecule

	// Detach the molecule
	_, err = b.DetachMolecule(pinnedBeadID)
	if err != nil {
		return fmt.Errorf("detaching molecule: %w", err)
	}

	fmt.Printf("%s Detached %s from %s\n", style.Bold.Render("✓"), previousMolecule, pinnedBeadID)

	return nil
}

func runMoleculeAttachment(cmd *cobra.Command, args []string) error {
	pinnedBeadID := args[0]

	workDir, err := findLocalBeadsDir()
	if err != nil {
		return fmt.Errorf("not in a beads workspace: %w", err)
	}

	b := beads.New(workDir)

	// Get the issue
	issue, err := b.Show(pinnedBeadID)
	if err != nil {
		return fmt.Errorf("getting issue: %w", err)
	}

	attachment := beads.ParseAttachmentFields(issue)

	if moleculeJSON {
		type attachmentOutput struct {
			IssueID          string `json:"issue_id"`
			IssueTitle       string `json:"issue_title"`
			Status           string `json:"status"`
			AttachedMolecule string `json:"attached_molecule,omitempty"`
			AttachedAt       string `json:"attached_at,omitempty"`
		}
		out := attachmentOutput{
			IssueID:    issue.ID,
			IssueTitle: issue.Title,
			Status:     issue.Status,
		}
		if attachment != nil {
			out.AttachedMolecule = attachment.AttachedMolecule
			out.AttachedAt = attachment.AttachedAt
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	// Human-readable output
	fmt.Printf("\n%s: %s\n", style.Bold.Render(issue.ID), issue.Title)
	fmt.Printf("Status: %s\n", issue.Status)

	if attachment == nil || attachment.AttachedMolecule == "" {
		fmt.Printf("\n%s\n", style.Dim.Render("No molecule attached"))
	} else {
		fmt.Printf("\n%s\n", style.Bold.Render("Attached Molecule:"))
		fmt.Printf("  ID: %s\n", attachment.AttachedMolecule)
		if attachment.AttachedAt != "" {
			fmt.Printf("  Attached at: %s\n", attachment.AttachedAt)
		}
	}

	return nil
}
