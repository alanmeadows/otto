package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReverseBranchTemplateMapping(t *testing.T) {
	tests := []struct {
		name     string
		template string
		branch   string
		expected string
		wantErr  bool
	}{
		{"standard", "otto/{{.Name}}", "otto/feature-1", "feature-1", false},
		{"complex", "work/{{.Name}}/dev", "work/task-42/dev", "task-42", false},
		{"default", "", "otto/test-branch", "test-branch", false},
		{"mismatch", "otto/{{.Name}}", "other/branch", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, err := ReverseBranchTemplate(tt.template, tt.branch)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, name)
			}
		})
	}
}

// initGitRepoWithRemote creates a git repo with a remote set and a branch.
func initGitRepoWithRemote(t *testing.T, dir, remoteURL string) {
	t.Helper()
	initGitRepo(t, dir)
	cmd := exec.Command("git", "remote", "add", "origin", remoteURL)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git remote add: %s", string(out))
}

func TestMapPRToWorkDir_WorktreeStrategy(t *testing.T) {
	dir := t.TempDir()
	primaryDir := filepath.Join(dir, "primary")
	worktreeDir := filepath.Join(dir, "worktrees")
	require.NoError(t, os.MkdirAll(primaryDir, 0755))

	remoteURL := "https://github.com/test-org/test-repo.git"
	initGitRepoWithRemote(t, primaryDir, remoteURL)

	// Create a branch that simulates a PR branch
	branchCmd := exec.Command("git", "branch", "otto/feature-pr")
	branchCmd.Dir = primaryDir
	require.NoError(t, branchCmd.Run())

	cfg := &config.Config{
		Repos: []config.RepoConfig{
			{
				Name:           "test-repo",
				PrimaryDir:     primaryDir,
				WorktreeDir:    worktreeDir,
				GitStrategy:    config.GitStrategyWorktree,
				BranchTemplate: "otto/{{.Name}}",
			},
		},
	}

	workDir, cleanup, err := MapPRToWorkDir(cfg, remoteURL, "otto/feature-pr")
	require.NoError(t, err)
	assert.NotEmpty(t, workDir)
	assert.DirExists(t, workDir)
	assert.NotNil(t, cleanup)

	// Cleanup should remove the worktree
	cleanup()
}

func TestMapPRToWorkDir_HandsOffStrategy_Match(t *testing.T) {
	dir := t.TempDir()
	primaryDir := filepath.Join(dir, "primary")
	require.NoError(t, os.MkdirAll(primaryDir, 0755))

	remoteURL := "https://github.com/test-org/test-repo.git"
	initGitRepoWithRemote(t, primaryDir, remoteURL)

	// Get current branch name
	branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	branchCmd.Dir = primaryDir
	out, err := branchCmd.Output()
	require.NoError(t, err)
	currentBranch := string(out[:len(out)-1]) // trim newline

	cfg := &config.Config{
		Repos: []config.RepoConfig{
			{
				Name:        "test-repo",
				PrimaryDir:  primaryDir,
				GitStrategy: config.GitStrategyHandsOff,
			},
		},
	}

	workDir, cleanup, err := MapPRToWorkDir(cfg, remoteURL, currentBranch)
	require.NoError(t, err)
	assert.Equal(t, primaryDir, workDir)
	assert.Nil(t, cleanup)
}

func TestMapPRToWorkDir_HandsOffStrategy_Mismatch(t *testing.T) {
	dir := t.TempDir()
	primaryDir := filepath.Join(dir, "primary")
	require.NoError(t, os.MkdirAll(primaryDir, 0755))

	remoteURL := "https://github.com/test-org/test-repo.git"
	initGitRepoWithRemote(t, primaryDir, remoteURL)

	cfg := &config.Config{
		Repos: []config.RepoConfig{
			{
				Name:        "test-repo",
				PrimaryDir:  primaryDir,
				GitStrategy: config.GitStrategyHandsOff,
			},
		},
	}

	_, _, err := MapPRToWorkDir(cfg, remoteURL, "nonexistent-branch")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not match")
}

func TestMapPRToWorkDir_BranchStrategy(t *testing.T) {
	dir := t.TempDir()
	primaryDir := filepath.Join(dir, "primary")
	require.NoError(t, os.MkdirAll(primaryDir, 0755))

	remoteURL := "https://github.com/test-org/test-repo.git"
	initGitRepoWithRemote(t, primaryDir, remoteURL)

	// Create a branch to simulate PR branch
	branchCmd := exec.Command("git", "branch", "feature-branch")
	branchCmd.Dir = primaryDir
	require.NoError(t, branchCmd.Run())

	cfg := &config.Config{
		Repos: []config.RepoConfig{
			{
				Name:           "test-repo",
				PrimaryDir:     primaryDir,
				GitStrategy:    config.GitStrategyBranch,
				BranchTemplate: "otto/{{.Name}}",
			},
		},
	}

	workDir, cleanup, err := MapPRToWorkDir(cfg, remoteURL, "feature-branch")
	require.NoError(t, err)
	assert.Equal(t, primaryDir, workDir)
	assert.Nil(t, cleanup)
}

func TestMapPRToWorkDir_RepoNotFound(t *testing.T) {
	cfg := &config.Config{}
	_, _, err := MapPRToWorkDir(cfg, "https://github.com/nope/nope.git", "main")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "finding repo")
}
