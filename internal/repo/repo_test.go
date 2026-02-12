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

func TestManagerAddAndList(t *testing.T) {
	dir := t.TempDir()
	primaryDir := filepath.Join(dir, "myrepo")
	require.NoError(t, os.MkdirAll(primaryDir, 0755))

	mgr := NewManager(dir)
	cfg := &config.Config{}

	repo := config.RepoConfig{
		Name:           "test-repo",
		PrimaryDir:     primaryDir,
		GitStrategy:    config.GitStrategyWorktree,
		BranchTemplate: "otto/{{.Name}}",
	}

	err := mgr.Add(cfg, repo)
	require.NoError(t, err)
	assert.Len(t, cfg.Repos, 1)
	assert.Equal(t, "test-repo", cfg.Repos[0].Name)

	// List
	repos := mgr.List(cfg)
	assert.Len(t, repos, 1)
}

func TestManagerAddDuplicate(t *testing.T) {
	dir := t.TempDir()
	primaryDir := filepath.Join(dir, "myrepo")
	require.NoError(t, os.MkdirAll(primaryDir, 0755))

	mgr := NewManager(dir)
	cfg := &config.Config{}

	repo := config.RepoConfig{
		Name:       "test-repo",
		PrimaryDir: primaryDir,
	}

	require.NoError(t, mgr.Add(cfg, repo))
	err := mgr.Add(cfg, repo)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestManagerAddInvalidDir(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)
	cfg := &config.Config{}

	repo := config.RepoConfig{
		Name:       "test-repo",
		PrimaryDir: "/nonexistent/path",
	}

	err := mgr.Add(cfg, repo)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

func TestManagerRemove(t *testing.T) {
	dir := t.TempDir()
	primaryDir := filepath.Join(dir, "myrepo")
	require.NoError(t, os.MkdirAll(primaryDir, 0755))

	mgr := NewManager(dir)
	cfg := &config.Config{}

	repo := config.RepoConfig{
		Name:       "test-repo",
		PrimaryDir: primaryDir,
	}

	require.NoError(t, mgr.Add(cfg, repo))
	require.Len(t, cfg.Repos, 1)

	err := mgr.Remove(cfg, "test-repo")
	require.NoError(t, err)
	assert.Len(t, cfg.Repos, 0)
}

func TestManagerRemoveNotFound(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)
	cfg := &config.Config{}

	err := mgr.Remove(cfg, "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestManagerFindByRemoteURL(t *testing.T) {
	dir := t.TempDir()
	primaryDir := filepath.Join(dir, "myrepo")
	require.NoError(t, os.MkdirAll(primaryDir, 0755))

	// Init git repo with a remote
	initGitRepo(t, primaryDir)
	cmd := exec.Command("git", "remote", "add", "origin", "https://github.com/test-org/test-repo.git")
	cmd.Dir = primaryDir
	require.NoError(t, cmd.Run())

	mgr := NewManager(dir)
	cfg := &config.Config{
		Repos: []config.RepoConfig{
			{
				Name:       "test-repo",
				PrimaryDir: primaryDir,
			},
		},
	}

	// Match by HTTPS URL
	found, err := mgr.FindByRemoteURL(cfg, "https://github.com/test-org/test-repo.git")
	require.NoError(t, err)
	assert.Equal(t, "test-repo", found.Name)

	// Match by SSH URL (normalization)
	found, err = mgr.FindByRemoteURL(cfg, "git@github.com:test-org/test-repo.git")
	require.NoError(t, err)
	assert.Equal(t, "test-repo", found.Name)

	// No match
	_, err = mgr.FindByRemoteURL(cfg, "https://github.com/other/repo.git")
	assert.Error(t, err)
}

func TestNormalizeGitURLVariants(t *testing.T) {
	// Additional edge cases beyond the base test
	tests := []struct {
		input    string
		expected string
	}{
		{"https://github.com/org/repo/", "github.com/org/repo"},
		{"  git@github.com:org/repo.git  ", "github.com/org/repo"},
		// ADO visualstudio.com → dev.azure.com normalization
		{"https://msazure.visualstudio.com/DefaultCollection/One/_git/azlocal-overlay", "dev.azure.com/msazure/one/_git/azlocal-overlay"},
		{"https://msazure.visualstudio.com/One/_git/azlocal-overlay", "dev.azure.com/msazure/one/_git/azlocal-overlay"},
		{"https://dev.azure.com/msazure/One/_git/azlocal-overlay", "dev.azure.com/msazure/one/_git/azlocal-overlay"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeGitURL(tt.input))
		})
	}
}

func TestStripPRPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"ado PR URL", "https://dev.azure.com/msazure/One/_git/azlocal-overlay/pullrequest/14708380", "https://dev.azure.com/msazure/One/_git/azlocal-overlay"},
		{"github PR URL", "https://github.com/org/repo/pull/42", "https://github.com/org/repo"},
		{"plain repo URL", "https://github.com/org/repo", "https://github.com/org/repo"},
		{"ado repo URL no PR", "https://dev.azure.com/msazure/One/_git/azlocal-overlay", "https://dev.azure.com/msazure/One/_git/azlocal-overlay"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, stripPRPath(tt.input))
		})
	}
}

func TestFindByRemoteURL_WithPRURL(t *testing.T) {
	dir := t.TempDir()
	primaryDir := filepath.Join(dir, "myrepo")
	require.NoError(t, os.MkdirAll(primaryDir, 0755))

	initGitRepo(t, primaryDir)
	cmd := exec.Command("git", "remote", "add", "origin", "https://dev.azure.com/msazure/One/_git/azlocal-overlay")
	cmd.Dir = primaryDir
	require.NoError(t, cmd.Run())

	mgr := NewManager(dir)
	cfg := &config.Config{
		Repos: []config.RepoConfig{
			{Name: "overlay", PrimaryDir: primaryDir},
		},
	}

	// Should match even when the URL has /pullrequest/ID appended.
	found, err := mgr.FindByRemoteURL(cfg, "https://dev.azure.com/msazure/One/_git/azlocal-overlay/pullrequest/14708380")
	require.NoError(t, err)
	assert.Equal(t, "overlay", found.Name)
}

func TestFindByRemoteURL_CrossDomainADO(t *testing.T) {
	dir := t.TempDir()
	primaryDir := filepath.Join(dir, "myrepo")
	require.NoError(t, os.MkdirAll(primaryDir, 0755))

	// Repo uses visualstudio.com remote (common for older ADO repos).
	initGitRepo(t, primaryDir)
	cmd := exec.Command("git", "remote", "add", "origin", "https://msazure.visualstudio.com/DefaultCollection/One/_git/azlocal-overlay")
	cmd.Dir = primaryDir
	require.NoError(t, cmd.Run())

	mgr := NewManager(dir)
	cfg := &config.Config{
		Repos: []config.RepoConfig{
			{Name: "overlay", PrimaryDir: primaryDir},
		},
	}

	// PR URL uses dev.azure.com — should still match after normalization.
	found, err := mgr.FindByRemoteURL(cfg, "https://dev.azure.com/msazure/One/_git/azlocal-overlay/pullrequest/14708380")
	require.NoError(t, err)
	assert.Equal(t, "overlay", found.Name)
}
