package repo

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/tidwall/jsonc"
)

// Manager handles repository configuration management.
type Manager struct {
	configDir string // user config dir (~/.config/otto)
}

// NewManager creates a new repository manager.
func NewManager(configDir string) *Manager {
	return &Manager{configDir: filepath.Join(configDir, "otto")}
}

// Add validates and appends a repo to the config, writing back to user config.
func (m *Manager) Add(cfg *config.Config, repo config.RepoConfig) error {
	// Validate primary dir exists
	info, err := os.Stat(repo.PrimaryDir)
	if err != nil {
		return fmt.Errorf("primary directory %q does not exist: %w", repo.PrimaryDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("primary directory %q is not a directory", repo.PrimaryDir)
	}

	// Check for duplicate name
	for _, r := range cfg.Repos {
		if r.Name == repo.Name {
			return fmt.Errorf("repository %q already exists", repo.Name)
		}
	}

	cfg.Repos = append(cfg.Repos, repo)
	return m.writeUserConfig(cfg)
}

// Remove removes a repo by name from the config.
func (m *Manager) Remove(cfg *config.Config, name string) error {
	found := false
	repos := make([]config.RepoConfig, 0, len(cfg.Repos))
	for _, r := range cfg.Repos {
		if r.Name == name {
			found = true
			continue
		}
		repos = append(repos, r)
	}
	if !found {
		return fmt.Errorf("repository %q not found", name)
	}
	cfg.Repos = repos
	return m.writeUserConfig(cfg)
}

// List returns all configured repositories.
func (m *Manager) List(cfg *config.Config) []config.RepoConfig {
	return cfg.Repos
}

// FindByRemoteURL finds a repo by matching its git remote URL.
func (m *Manager) FindByRemoteURL(cfg *config.Config, remoteURL string) (*config.RepoConfig, error) {
	normalizedTarget := normalizeGitURL(stripPRPath(remoteURL))

	for i := range cfg.Repos {
		r := &cfg.Repos[i]
		repoRemote, err := getRemoteURL(r.PrimaryDir)
		if err != nil {
			continue
		}
		if normalizeGitURL(repoRemote) == normalizedTarget {
			return r, nil
		}
	}
	return nil, fmt.Errorf("no repository found matching remote URL %q", remoteURL)
}

// FindByCWD finds a repo matching the current working directory's git remote.
func (m *Manager) FindByCWD(cfg *config.Config) (*config.RepoConfig, error) {
	remoteURL, err := getRemoteURL("")
	if err != nil {
		return nil, fmt.Errorf("getting remote URL for CWD: %w", err)
	}
	return m.FindByRemoteURL(cfg, remoteURL)
}

// writeUserConfig writes the repo list to the user config file.
func (m *Manager) writeUserConfig(cfg *config.Config) error {
	if err := os.MkdirAll(m.configDir, 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	configPath := filepath.Join(m.configDir, "otto.jsonc")

	// Read existing config or create new
	var existing map[string]any
	if data, err := os.ReadFile(configPath); err == nil {
		// Strip JSONC comments before parsing
		jsonData := jsonc.ToJSON(data)
		if err := json.Unmarshal(jsonData, &existing); err != nil {
			existing = make(map[string]any)
		}
	} else {
		existing = make(map[string]any)
	}

	// Marshal repos to update
	reposJSON, err := json.Marshal(cfg.Repos)
	if err != nil {
		return fmt.Errorf("marshaling repos: %w", err)
	}
	var reposAny any
	if err := json.Unmarshal(reposJSON, &reposAny); err != nil {
		return fmt.Errorf("unmarshaling repos: %w", err)
	}
	existing["repos"] = reposAny

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	return os.WriteFile(configPath, data, 0644)
}

// getRemoteURL gets the origin remote URL for a directory.
// If dir is empty, uses the current working directory.
func getRemoteURL(dir string) (string, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git remote get-url origin: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// stripPRPath removes PR-specific path segments from a URL so it can be matched
// against a git remote.  Handles ADO (/pullrequest/{id}) and GitHub (/pull/{id}).
func stripPRPath(url string) string {
	// ADO: …/_git/repo/pullrequest/12345 → …/_git/repo
	if idx := strings.Index(url, "/pullrequest/"); idx != -1 {
		return url[:idx]
	}
	// GitHub: …/owner/repo/pull/42 → …/owner/repo
	if idx := strings.Index(url, "/pull/"); idx != -1 {
		return url[:idx]
	}
	return url
}

// normalizeGitURL normalizes a git URL for comparison.
// Strips .git suffix and extracts host/path for comparison.
// Also normalizes Azure DevOps URL variants to a canonical form.
func normalizeGitURL(url string) string {
	url = strings.TrimSpace(url)
	url = strings.TrimSuffix(url, ".git")

	// Handle SSH URLs: git@host:owner/repo → host/owner/repo
	if strings.HasPrefix(url, "git@") {
		url = strings.TrimPrefix(url, "git@")
		url = strings.Replace(url, ":", "/", 1)
	}

	// Handle HTTPS URLs: https://host/owner/repo → host/owner/repo
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")

	// Normalize trailing slash
	url = strings.TrimSuffix(url, "/")

	url = strings.ToLower(url)

	// Normalize Azure DevOps URL variants to a canonical form.
	// {org}.visualstudio.com/DefaultCollection/{project}/_git/{repo}
	//   → dev.azure.com/{org}/{project}/_git/{repo}
	// {org}.visualstudio.com/{project}/_git/{repo}
	//   → dev.azure.com/{org}/{project}/_git/{repo}
	if strings.Contains(url, ".visualstudio.com") {
		// Extract org from hostname: {org}.visualstudio.com/...
		parts := strings.SplitN(url, ".visualstudio.com/", 2)
		if len(parts) == 2 {
			org := parts[0]
			path := parts[1]
			// Strip optional DefaultCollection/ prefix
			path = strings.TrimPrefix(path, "defaultcollection/")
			url = "dev.azure.com/" + org + "/" + path
		}
	}

	return url
}
