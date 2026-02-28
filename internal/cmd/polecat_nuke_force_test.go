package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/tmux"
)

// TestNukePolecatFull_UnmergedCommitsWithoutForce tests that unmerged commits
// prevent remote branch deletion when --force is NOT set.
func TestNukePolecatFull_UnmergedCommitsWithoutForce(t *testing.T) {
	tmpDir := t.TempDir()
	rigName := "testrig"
	polecatName := "tester"

	// Create bare origin repo (this simulates the "remote" origin)
	originDir := filepath.Join(tmpDir, "origin.git")
	if err := os.MkdirAll(originDir, 0755); err != nil {
		t.Fatal(err)
	}
	run(t, originDir, "git", "init", "--bare")

	// Create rig directory structure
	rigDir := filepath.Join(tmpDir, rigName)
	polecatsDir := filepath.Join(rigDir, "polecats")
	if err := os.MkdirAll(polecatsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create polecat worktree
	worktreePath := filepath.Join(polecatsDir, polecatName)
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		t.Fatal(err)
	}
	run(t, worktreePath, "git", "init")
	run(t, worktreePath, "git", "config", "user.email", "test@test.com")
	run(t, worktreePath, "git", "config", "user.name", "Test User")
	run(t, worktreePath, "git", "remote", "add", "origin", originDir)

	// Create initial commit on main and push to origin
	writeFile(t, filepath.Join(worktreePath, "README.md"), "# test\n")
	run(t, worktreePath, "git", "add", ".")
	run(t, worktreePath, "git", "commit", "-m", "initial commit")
	run(t, worktreePath, "git", "branch", "-M", "main")
	run(t, worktreePath, "git", "push", "-u", "origin", "main")

	// Create polecat branch with unmerged commits
	branchName := "polecat/" + polecatName + "-work"
	run(t, worktreePath, "git", "checkout", "-b", branchName)
	writeFile(t, filepath.Join(worktreePath, "feature.go"), "package feature\n")
	run(t, worktreePath, "git", "add", ".")
	run(t, worktreePath, "git", "commit", "-m", "feat: unmerged work")
	run(t, worktreePath, "git", "push", "-u", "origin", branchName)

	// Create bare repo for the rig (.repo.git) - this simulates the rig's view of the repo
	bareRepoPath := filepath.Join(rigDir, ".repo.git")
	if err := os.MkdirAll(bareRepoPath, 0755); err != nil {
		t.Fatal(err)
	}
	run(t, bareRepoPath, "git", "init", "--bare")
	// Add origin as a remote so fetch works
	run(t, bareRepoPath, "git", "remote", "add", "origin", originDir)
	// Fetch from origin to get all branches
	run(t, bareRepoPath, "git", "fetch", "origin")

	// Set up polecat manager and rig
	r := &rig.Rig{
		Name: rigName,
		Path: rigDir,
	}
	repoGit := git.NewGitWithDir(bareRepoPath, "")
	tm := tmux.NewTmux()
	mgr := polecat.NewManager(r, repoGit, tm)

	// Ensure force flag is false (clean state)
	oldForce := polecatNukeForce
	polecatNukeForce = false
	defer func() { polecatNukeForce = oldForce }()

	// Run nukePolecatFull - should NOT delete remote branch due to unmerged commits
	_ = nukePolecatFull(polecatName, rigName, mgr, r)

	// Verify remote branch still exists (was NOT deleted due to unmerged commits)
	exists, err := remoteBranchExistsOnBareRepo(originDir, branchName)
	if err != nil {
		t.Fatalf("failed to check remote branch: %v", err)
	}
	if !exists {
		t.Errorf("remote branch %q should have been preserved (has unmerged commits), but was deleted", branchName)
	}
}

// TestNukePolecatFull_UnmergedCommitsWithForce tests that --force bypasses
// the unmerged commits check and deletes the remote branch anyway.
func TestNukePolecatFull_UnmergedCommitsWithForce(t *testing.T) {
	tmpDir := t.TempDir()
	rigName := "testrig"
	polecatName := "tester"

	// Create bare origin repo (this simulates the "remote" origin)
	originDir := filepath.Join(tmpDir, "origin.git")
	if err := os.MkdirAll(originDir, 0755); err != nil {
		t.Fatal(err)
	}
	run(t, originDir, "git", "init", "--bare")

	// Create rig directory structure
	rigDir := filepath.Join(tmpDir, rigName)
	polecatsDir := filepath.Join(rigDir, "polecats")
	if err := os.MkdirAll(polecatsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create polecat worktree
	worktreePath := filepath.Join(polecatsDir, polecatName)
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		t.Fatal(err)
	}
	run(t, worktreePath, "git", "init")
	run(t, worktreePath, "git", "config", "user.email", "test@test.com")
	run(t, worktreePath, "git", "config", "user.name", "Test User")
	run(t, worktreePath, "git", "remote", "add", "origin", originDir)

	// Create initial commit on main and push to origin
	writeFile(t, filepath.Join(worktreePath, "README.md"), "# test\n")
	run(t, worktreePath, "git", "add", ".")
	run(t, worktreePath, "git", "commit", "-m", "initial commit")
	run(t, worktreePath, "git", "branch", "-M", "main")
	run(t, worktreePath, "git", "push", "-u", "origin", "main")

	// Create polecat branch with unmerged commits
	branchName := "polecat/" + polecatName + "-work"
	run(t, worktreePath, "git", "checkout", "-b", branchName)
	writeFile(t, filepath.Join(worktreePath, "feature.go"), "package feature\n")
	run(t, worktreePath, "git", "add", ".")
	run(t, worktreePath, "git", "commit", "-m", "feat: unmerged work")
	run(t, worktreePath, "git", "push", "-u", "origin", branchName)

	// Create bare repo for the rig (.repo.git) - this simulates the rig's view of the repo
	bareRepoPath := filepath.Join(rigDir, ".repo.git")
	if err := os.MkdirAll(bareRepoPath, 0755); err != nil {
		t.Fatal(err)
	}
	run(t, bareRepoPath, "git", "init", "--bare")
	// Add origin as a remote so fetch works
	run(t, bareRepoPath, "git", "remote", "add", "origin", originDir)
	// Fetch from origin to get all branches
	run(t, bareRepoPath, "git", "fetch", "origin")

	// Set up polecat manager and rig
	r := &rig.Rig{
		Name: rigName,
		Path: rigDir,
	}
	repoGit := git.NewGitWithDir(bareRepoPath, "")
	tm := tmux.NewTmux()
	mgr := polecat.NewManager(r, repoGit, tm)

	// Set force flag to true
	oldForce := polecatNukeForce
	polecatNukeForce = true
	defer func() { polecatNukeForce = oldForce }()

	// Run nukePolecatFull - should delete remote branch despite unmerged commits
	_ = nukePolecatFull(polecatName, rigName, mgr, r)

	// Verify remote branch was deleted (force bypassed the unmerged check)
	exists, err := remoteBranchExistsOnBareRepo(originDir, branchName)
	if err != nil {
		t.Fatalf("failed to check remote branch: %v", err)
	}
	if exists {
		t.Errorf("remote branch %q should have been deleted (--force was set), but still exists", branchName)
	}
}

// remoteBranchExistsOnBareRepo checks if a branch exists in a bare repository.
func remoteBranchExistsOnBareRepo(bareRepoPath, branchName string) (bool, error) {
	repoGit := git.NewGitWithDir(bareRepoPath, "")
	return repoGit.BranchExists(branchName)
}
