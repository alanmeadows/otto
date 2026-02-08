package repo

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alanmeadows/otto/internal/config"
)

// MapPRToWorkDir maps a PR's repo URL and branch to a local working directory.
// Returns the working directory path, an optional cleanup function, and any error.
func MapPRToWorkDir(cfg *config.Config, repoURL, branchName string) (workDir string, cleanup func(), err error) {
	// Find matching repo
	mgr := NewManager("")
	repo, err := mgr.FindByRemoteURL(cfg, repoURL)
	if err != nil {
		return "", nil, fmt.Errorf("finding repo for URL %q: %w", repoURL, err)
	}

	strategy := NewStrategy(*repo)

	switch repo.GitStrategy {
	case config.GitStrategyWorktree:
		// Extract logical name from branch for the worktree directory name
		name, err := ReverseBranchTemplate(repo.BranchTemplate, branchName)
		if err != nil {
			// Fall back to using branch name directly
			name = branchName
		}

		// Fetch remote refs so the branch is available locally
		fetchCmd := exec.Command("git", "fetch", "origin", branchName)
		fetchCmd.Dir = repo.PrimaryDir
		_ = fetchCmd.Run() // best effort — branch may already be fetched

		// Check out an existing remote branch into a worktree (no -b flag)
		workDir, err = checkoutWorktree(repo, name, branchName)
		if err != nil {
			return "", nil, fmt.Errorf("creating worktree for branch %q: %w", branchName, err)
		}

		cleanup = func() {
			_ = strategy.Remove(name, true)
		}
		return workDir, cleanup, nil

	case config.GitStrategyBranch:
		// Fetch and checkout the existing PR branch
		fetchCmd := exec.Command("git", "fetch", "origin", branchName)
		fetchCmd.Dir = repo.PrimaryDir
		_ = fetchCmd.Run() // best effort

		checkoutCmd := exec.Command("git", "checkout", branchName)
		checkoutCmd.Dir = repo.PrimaryDir
		if out, err := checkoutCmd.CombinedOutput(); err != nil {
			// Try tracking the remote branch
			checkoutCmd = exec.Command("git", "checkout", "-b", branchName, "origin/"+branchName)
			checkoutCmd.Dir = repo.PrimaryDir
			if out2, err2 := checkoutCmd.CombinedOutput(); err2 != nil {
				return "", nil, fmt.Errorf("checking out branch %q: %s / %s: %w", branchName, string(out), string(out2), err2)
			}
		}
		return repo.PrimaryDir, nil, nil

	case config.GitStrategyHandsOff:
		currentBranch, err := strategy.CurrentBranch()
		if err != nil {
			return "", nil, fmt.Errorf("getting current branch: %w", err)
		}
		if currentBranch != branchName {
			return "", nil, fmt.Errorf("hands-off strategy: current branch %q does not match requested %q", currentBranch, branchName)
		}
		return repo.PrimaryDir, nil, nil

	default:
		return "", nil, fmt.Errorf("unknown git strategy %q", repo.GitStrategy)
	}
}

// checkoutWorktree checks out an existing branch into a worktree directory.
// Unlike CreateBranch, this does NOT use -b (no new branch creation).
func checkoutWorktree(repo *config.RepoConfig, name, branchName string) (string, error) {
	worktreeDir := repo.WorktreeDir
	if worktreeDir == "" {
		worktreeDir = filepath.Join(filepath.Dir(repo.PrimaryDir), "worktrees")
	}
	workDir := filepath.Join(worktreeDir, name)

	// First try: checkout existing branch directly
	cmd := exec.Command("git", "worktree", "add", workDir, branchName)
	cmd.Dir = repo.PrimaryDir
	if out, err := cmd.CombinedOutput(); err != nil {
		// Second try: use remote tracking ref
		remoteBranch := "origin/" + branchName
		cmd2 := exec.Command("git", "worktree", "add", workDir, remoteBranch)
		cmd2.Dir = repo.PrimaryDir
		if out2, err2 := cmd2.CombinedOutput(); err2 != nil {
			return "", fmt.Errorf("git worktree add: %s / %s: %w",
				strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)), err2)
		}
	}

	return workDir, nil
}
