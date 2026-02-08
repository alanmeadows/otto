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
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeGitURL(tt.input))
		})
	}
}
