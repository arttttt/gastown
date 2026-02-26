// Package beads provides merge request and gate utilities.
package beads

import (
	"encoding/json"
	"strings"
)

// FindMRForBranch searches for an existing merge-request bead for the given branch.
// Returns the MR bead if found, nil if not found.
// This enables idempotent `gt done` - if an MR already exists, we skip creation.
func (b *Beads) FindMRForBranch(branch string) (*Issue, error) {
	branchPrefix := "branch: " + branch + "\n"

	// Check issues table first (non-ephemeral MR beads)
	issues, err := b.List(ListOptions{
		Status: "open",
		Label:  "gt:merge-request",
	})
	if err != nil {
		return nil, err
	}
	for _, issue := range issues {
		if strings.HasPrefix(issue.Description, branchPrefix) {
			return issue, nil
		}
	}

	// Also check the wisps table: MR beads are created with --ephemeral so they
	// live in the wisps table (SQLite), not the issues table (Dolt). bd list only
	// queries issues, so we must query wisps separately or the existence check
	// always misses and retries hit a UNIQUE constraint.
	return b.findMRInWisps(branchPrefix), nil
}

// FindMRForBranchAny searches for a merge-request bead for the given branch
// across all statuses (open and closed). Used by recovery checks to determine
// if work was ever submitted to the merge queue. See #1035.
func (b *Beads) FindMRForBranchAny(branch string) (*Issue, error) {
	branchPrefix := "branch: " + branch + "\n"

	issues, err := b.List(ListOptions{
		Status: "all",
		Label:  "gt:merge-request",
	})
	if err != nil {
		return nil, err
	}
	for _, issue := range issues {
		if strings.HasPrefix(issue.Description, branchPrefix) {
			return issue, nil
		}
	}

	// Also check wisps table (ephemeral MR beads not visible to bd list)
	return b.findMRInWisps(branchPrefix), nil
}

// findMRInWisps searches the wisps table for a merge-request bead matching branchPrefix.
// Returns nil if not found or if the wisps table is unavailable.
func (b *Beads) findMRInWisps(branchPrefix string) *Issue {
	out, err := b.run("mol", "wisp", "list", "--json")
	if err != nil {
		return nil // Wisps table may not exist yet
	}

	var wrapper struct {
		Wisps []*Issue `json:"wisps"`
	}
	if err := json.Unmarshal(out, &wrapper); err != nil {
		return nil
	}

	for _, w := range wrapper.Wisps {
		if strings.HasPrefix(w.Description, branchPrefix) {
			return w
		}
	}

	return nil
}
