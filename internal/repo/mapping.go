package repo

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alanmeadows/otto/internal/config"
)

// MapPRToWorkDir maps a PR's repo URL and branch to a local working directory.
// Returns the working directory path, an optional cleanup function, and any error.
func MapPRToWorkDir(cfg *config.Config, repoURL, branchName string) (workDir string, cleanup func(), err error) {
	// Strip refs/heads/ prefix — ADO stores full refspecs but git locally uses
	// short branch names.
	shortBranch := strings.TrimPrefix(branchName, "refs/heads/")

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
		name, err := ReverseBranchTemplate(repo.BranchTemplate, shortBranch)
		if err != nil {
			// Fall back to using branch name directly
			name = shortBranch
		}

		// Compute the expected worktree path.
		wtDir := repo.WorktreeDir
		if wtDir == "" {
			wtDir = filepath.Join(filepath.Dir(repo.PrimaryDir), "worktrees")
		}
		expectedDir := filepath.Join(wtDir, name)

		// If the worktree already exists and is on the right branch, reuse it.
		if info, statErr := os.Stat(expectedDir); statErr == nil && info.IsDir() {
			// Verify it's on the expected branch.
			branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
			branchCmd.Dir = expectedDir
			if brOut, brErr := branchCmd.Output(); brErr == nil {
				currentBr := strings.TrimSpace(string(brOut))
				if currentBr == shortBranch {
					// Pull latest changes from origin.
					pullCmd := exec.Command("git", "pull", "--ff-only", "origin", shortBranch)
					pullCmd.Dir = expectedDir
					_ = pullCmd.Run() // best effort
					// Return with NO cleanup — this is the user's existing worktree.
					return expectedDir, nil, nil
				}
			}
		}

		// Fetch remote refs so the branch is available locally
		fetchCmd := exec.Command("git", "fetch", "origin", shortBranch)
		fetchCmd.Dir = repo.PrimaryDir
		_ = fetchCmd.Run() // best effort — branch may already be fetched

		// Check out an existing remote branch into a worktree
		workDir, err = checkoutWorktree(repo, name, shortBranch)
		if err != nil {
			return "", nil, fmt.Errorf("creating worktree for branch %q: %w", shortBranch, err)
		}

		// Only clean up worktrees we created (not pre-existing ones).
		cleanup = func() {
			_ = strategy.Remove(name, true)
		}
		return workDir, cleanup, nil

	case config.GitStrategyBranch:
		// Fetch and checkout the existing PR branch
		fetchCmd := exec.Command("git", "fetch", "origin", shortBranch)
		fetchCmd.Dir = repo.PrimaryDir
		_ = fetchCmd.Run() // best effort

		checkoutCmd := exec.Command("git", "checkout", shortBranch)
		checkoutCmd.Dir = repo.PrimaryDir
		if out, err := checkoutCmd.CombinedOutput(); err != nil {
			// Try tracking the remote branch
			checkoutCmd = exec.Command("git", "checkout", "-b", shortBranch, "origin/"+shortBranch)
			checkoutCmd.Dir = repo.PrimaryDir
			if out2, err2 := checkoutCmd.CombinedOutput(); err2 != nil {
				return "", nil, fmt.Errorf("checking out branch %q: %s / %s: %w", shortBranch, string(out), string(out2), err2)
			}
		}
		return repo.PrimaryDir, nil, nil

	case config.GitStrategyHandsOff:
		currentBranch, err := strategy.CurrentBranch()
		if err != nil {
			return "", nil, fmt.Errorf("getting current branch: %w", err)
		}
		if currentBranch != shortBranch {
			return "", nil, fmt.Errorf("hands-off strategy: current branch %q does not match requested %q", currentBranch, shortBranch)
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

	// First try: checkout existing local branch directly
	cmd := exec.Command("git", "worktree", "add", workDir, branchName)
	cmd.Dir = repo.PrimaryDir
	if out, err := cmd.CombinedOutput(); err != nil {
		// Second try: create a local branch tracking the remote ref
		// Using -b ensures we get a proper branch (not detached HEAD).
		cmd2 := exec.Command("git", "worktree", "add", "-b", branchName, workDir, "origin/"+branchName)
		cmd2.Dir = repo.PrimaryDir
		if out2, err2 := cmd2.CombinedOutput(); err2 != nil {
			return "", fmt.Errorf("git worktree add: %s / %s: %w",
				strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)), err2)
		}
	}

	return workDir, nil
}

// MapPRToCleanWorkDir creates a fresh temporary worktree for PR fix work.
// Unlike MapPRToWorkDir, this always creates a clean checkout in /tmp using
// detached HEAD, ensuring no pre-existing dirty state can leak into commits.
//
// Returns:
//   - workDir: path to the clean temporary worktree
//   - mergeBack: callback to update the user's existing worktree after push
//     (best-effort; returns error if merge fails)
//   - cleanup: callback to remove the temporary worktree (always call via defer)
//   - err: any error during setup
func MapPRToCleanWorkDir(cfg *config.Config, repoURL, branchName string) (workDir string, mergeBack func() error, cleanup func(), err error) {
	shortBranch := strings.TrimPrefix(branchName, "refs/heads/")

	// Find matching repo.
	mgr := NewManager("")
	repo, err := mgr.FindByRemoteURL(cfg, repoURL)
	if err != nil {
		return "", nil, nil, fmt.Errorf("finding repo for URL %q: %w", repoURL, err)
	}

	// Fetch the branch so we have the latest remote state.
	fetchCmd := exec.Command("git", "fetch", "origin", shortBranch)
	fetchCmd.Dir = repo.PrimaryDir
	_ = fetchCmd.Run() // best effort

	// Create a temp directory for the worktree.
	tmpDir, err := os.MkdirTemp("", "otto-fix-*")
	if err != nil {
		return "", nil, nil, fmt.Errorf("creating temp dir: %w", err)
	}
	// git worktree add expects the target directory not to exist.
	os.Remove(tmpDir)

	// Create a detached HEAD worktree at the branch tip.
	// Detached HEAD avoids conflicting with the same branch being checked out
	// in the user's existing worktree.
	cmd := exec.Command("git", "worktree", "add", "--detach", tmpDir, "origin/"+shortBranch)
	cmd.Dir = repo.PrimaryDir
	if out, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
		os.RemoveAll(tmpDir)
		return "", nil, nil, fmt.Errorf("git worktree add --detach: %s: %w",
			strings.TrimSpace(string(out)), cmdErr)
	}

	slog.Info("created clean temporary worktree for PR fix",
		"branch", shortBranch,
		"tmpDir", tmpDir,
	)

	// Identify the user's existing worktree/checkout for merge-back.
	userWorkDir := findUserWorkDir(repo, shortBranch)
	if userWorkDir != "" {
		slog.Info("found user worktree for post-push merge-back",
			"branch", shortBranch,
			"userDir", userWorkDir,
		)
	}

	// mergeBack updates the user's existing worktree with the pushed changes.
	mergeBack = func() error {
		if userWorkDir == "" {
			return nil
		}
		fetchCmd := exec.Command("git", "fetch", "origin", shortBranch)
		fetchCmd.Dir = userWorkDir
		_ = fetchCmd.Run() // best effort

		mergeCmd := exec.Command("git", "merge", "--ff-only", "origin/"+shortBranch)
		mergeCmd.Dir = userWorkDir
		if out, mergeErr := mergeCmd.CombinedOutput(); mergeErr != nil {
			return fmt.Errorf("merge --ff-only in %s: %s: %w",
				userWorkDir, strings.TrimSpace(string(out)), mergeErr)
		}
		slog.Info("merged pushed changes back to user worktree",
			"branch", shortBranch,
			"userDir", userWorkDir,
		)
		return nil
	}

	// cleanup removes the temporary worktree.
	primaryDir := repo.PrimaryDir
	cleanup = func() {
		rmCmd := exec.Command("git", "worktree", "remove", tmpDir, "--force")
		rmCmd.Dir = primaryDir
		_ = rmCmd.Run()
		os.RemoveAll(tmpDir) // belt and suspenders
	}

	return tmpDir, mergeBack, cleanup, nil
}

// findUserWorkDir locates the user's existing checkout for a branch,
// so we can merge pushed changes back to it.
func findUserWorkDir(repo *config.RepoConfig, shortBranch string) string {
	switch repo.GitStrategy {
	case config.GitStrategyWorktree:
		name, err := ReverseBranchTemplate(repo.BranchTemplate, shortBranch)
		if err != nil {
			name = shortBranch
		}
		wtDir := repo.WorktreeDir
		if wtDir == "" {
			wtDir = filepath.Join(filepath.Dir(repo.PrimaryDir), "worktrees")
		}
		return verifyDirOnBranch(filepath.Join(wtDir, name), shortBranch)

	case config.GitStrategyBranch, config.GitStrategyHandsOff:
		return verifyDirOnBranch(repo.PrimaryDir, shortBranch)

	default:
		return ""
	}
}

// verifyDirOnBranch checks that dir exists and has the expected branch checked out.
func verifyDirOnBranch(dir, expectedBranch string) string {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return ""
	}
	branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	branchCmd.Dir = dir
	out, err := branchCmd.Output()
	if err != nil {
		return ""
	}
	if strings.TrimSpace(string(out)) == expectedBranch {
		return dir
	}
	return ""
}
