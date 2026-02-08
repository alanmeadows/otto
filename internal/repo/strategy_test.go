package repo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// initGitRepo creates a new git repo in the given directory with an initial commit.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()

	// Mark directory as safe (needed for WSL / temp dirs with different ownership)
	safeDirCmd := exec.Command("git", "config", "--global", "--add", "safe.directory", dir)
	_ = safeDirCmd.Run() // best effort

	commands := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "commit", "--allow-empty", "-m", "initial"},
	}
	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "command %v failed: %s", args, string(out))
	}
}

func TestRenderBranchName(t *testing.T) {
	tests := []struct {
		template string
		name     string
		expected string
	}{
		{"otto/{{.Name}}", "my-feature", "otto/my-feature"},
		{"feature/{{.Name}}/work", "task-1", "feature/task-1/work"},
		{"", "test", "otto/test"}, // default template
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result, err := renderBranchName(tt.template, tt.name)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestReverseBranchTemplate(t *testing.T) {
	tests := []struct {
		name     string
		template string
		branch   string
		expected string
		wantErr  bool
	}{
		{"simple", "otto/{{.Name}}", "otto/my-feature", "my-feature", false},
		{"with suffix", "feature/{{.Name}}/work", "feature/task-1/work", "task-1", false},
		{"default template", "", "otto/test", "test", false},
		{"no match prefix", "otto/{{.Name}}", "other/branch", "", true},
		{"no match suffix", "feature/{{.Name}}/work", "feature/task-1/play", "", true},
		{"empty name", "otto/{{.Name}}", "otto/", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ReverseBranchTemplate(tt.template, tt.branch)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestWorktreeStrategy(t *testing.T) {
	dir := t.TempDir()
	primaryDir := filepath.Join(dir, "primary")
	worktreeDir := filepath.Join(dir, "worktrees")
	require.NoError(t, os.MkdirAll(primaryDir, 0755))

	initGitRepo(t, primaryDir)

	repo := config.RepoConfig{
		Name:           "test-repo",
		PrimaryDir:     primaryDir,
		WorktreeDir:    worktreeDir,
		GitStrategy:    config.GitStrategyWorktree,
		BranchTemplate: "otto/{{.Name}}",
	}

	s := NewStrategy(repo)

	// Test create branch
	workDir, err := s.CreateBranch("", "feature-1")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(worktreeDir, "feature-1"), workDir)
	assert.DirExists(t, workDir)

	// Test list
	branches, err := s.List()
	require.NoError(t, err)
	assert.Len(t, branches, 2) // primary + worktree

	// Test current branch
	branch, err := s.CurrentBranch()
	require.NoError(t, err)
	assert.NotEmpty(t, branch)

	// Test remove
	err = s.Remove("feature-1", false)
	require.NoError(t, err)
}

func TestBranchStrategy(t *testing.T) {
	dir := t.TempDir()
	primaryDir := filepath.Join(dir, "primary")
	require.NoError(t, os.MkdirAll(primaryDir, 0755))

	initGitRepo(t, primaryDir)

	repo := config.RepoConfig{
		Name:           "test-repo",
		PrimaryDir:     primaryDir,
		GitStrategy:    config.GitStrategyBranch,
		BranchTemplate: "otto/{{.Name}}",
	}

	s := NewStrategy(repo)

	// Get initial branch name
	initialBranch, err := s.CurrentBranch()
	require.NoError(t, err)

	// Test create branch
	workDir, err := s.CreateBranch("", "feature-1")
	require.NoError(t, err)
	assert.Equal(t, primaryDir, workDir)

	// Verify we're on the new branch
	current, err := s.CurrentBranch()
	require.NoError(t, err)
	assert.Equal(t, "otto/feature-1", current)

	// Test list
	branches, err := s.List()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(branches), 2)

	// Switch back to initial branch before removing
	cmd := exec.Command("git", "checkout", initialBranch)
	cmd.Dir = primaryDir
	require.NoError(t, cmd.Run())

	// Test remove
	err = s.Remove("feature-1", false)
	require.NoError(t, err)
}

func TestHandsOffStrategy(t *testing.T) {
	dir := t.TempDir()
	primaryDir := filepath.Join(dir, "primary")
	require.NoError(t, os.MkdirAll(primaryDir, 0755))

	initGitRepo(t, primaryDir)

	repo := config.RepoConfig{
		Name:        "test-repo",
		PrimaryDir:  primaryDir,
		GitStrategy: config.GitStrategyHandsOff,
	}

	s := NewStrategy(repo)

	// Create should fail
	_, err := s.CreateBranch("", "feature-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "hands-off strategy does not create branches")

	// Remove should fail
	err = s.Remove("feature-1", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "hands-off strategy does not remove branches")

	// List should return current branch only
	branches, err := s.List()
	require.NoError(t, err)
	assert.Len(t, branches, 1)
	assert.True(t, branches[0].IsCurrent)

	// Current branch should work
	branch, err := s.CurrentBranch()
	require.NoError(t, err)
	assert.NotEmpty(t, branch)
}

func TestDirtyCheck(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Clean repo
	dirty, err := DirtyCheck(dir)
	require.NoError(t, err)
	assert.False(t, dirty)

	// Create a file to make it dirty
	err = os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("dirty"), 0644)
	require.NoError(t, err)

	dirty, err = DirtyCheck(dir)
	require.NoError(t, err)
	assert.True(t, dirty)
}

func TestNormalizeGitURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://github.com/user/repo.git", "github.com/user/repo"},
		{"https://github.com/user/repo", "github.com/user/repo"},
		{"git@github.com:user/repo.git", "github.com/user/repo"},
		{"git@github.com:user/repo", "github.com/user/repo"},
		{"http://github.com/User/Repo.git", "github.com/user/repo"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeGitURL(tt.input))
		})
	}
}

func TestNewStrategy(t *testing.T) {
	tests := []struct {
		strategy config.GitStrategy
		expected string
	}{
		{config.GitStrategyWorktree, "*repo.WorktreeStrategy"},
		{config.GitStrategyBranch, "*repo.BranchStrategy"},
		{config.GitStrategyHandsOff, "*repo.HandsOffStrategy"},
		{"unknown", "*repo.HandsOffStrategy"},
	}

	for _, tt := range tests {
		t.Run(string(tt.strategy), func(t *testing.T) {
			s := NewStrategy(config.RepoConfig{GitStrategy: tt.strategy})
			assert.NotNil(t, s)
			assert.Equal(t, tt.expected, fmt.Sprintf("%T", s))
		})
	}
}
