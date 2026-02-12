package opencode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PermissionConfig represents the OpenCode permission configuration.
type PermissionConfig struct {
	Permission map[string]string `json:"permission"`
}

// DefaultPermissions returns the default permission config for automated operation.
func DefaultPermissions() PermissionConfig {
	return PermissionConfig{
		Permission: map[string]string{
			"edit":               "allow",
			"bash":               "allow",
			"webfetch":           "allow",
			"websearch":          "allow",
			"read":               "allow",
			"glob":               "allow",
			"grep":               "allow",
			"list":               "allow",
			"task":               "allow",
			"skill":              "allow",
			"lsp":                "allow",
			"todoread":           "allow",
			"todowrite":          "allow",
			"codesearch":         "allow",
			"doom_loop":          "allow",
			"external_directory": "allow",
		},
	}
}

// EnsurePermissions writes the OpenCode permission config under .otto/ in
// the target directory and creates a relative symlink at the repo root so
// OpenCode can find it.  The actual content lives in .otto/opencode.json;
// the root-level opencode.json is just a symlink.
// Also adds both to .git/info/exclude so neither is tracked by git.
// Idempotent: safe to call multiple times.
func EnsurePermissions(directory string) error {
	cfg := DefaultPermissions()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling permission config: %w", err)
	}

	// Write the real file into .otto/
	ottoDir := filepath.Join(directory, ".otto")
	if err := os.MkdirAll(ottoDir, 0755); err != nil {
		return fmt.Errorf("creating .otto directory: %w", err)
	}
	realPath := filepath.Join(ottoDir, "opencode.json")
	if err := os.WriteFile(realPath, data, 0644); err != nil {
		return fmt.Errorf("writing permission config to %s: %w", realPath, err)
	}

	// Create a relative symlink at the repo root pointing into .otto/.
	// OpenCode looks for opencode.json in the project root.
	linkPath := filepath.Join(directory, "opencode.json")
	target := filepath.Join(".otto", "opencode.json") // relative

	// Remove any existing file/symlink so we can recreate it.
	if _, err := os.Lstat(linkPath); err == nil {
		os.Remove(linkPath)
	}
	if err := os.Symlink(target, linkPath); err != nil {
		// Fallback: if symlinking fails (e.g. filesystem doesn't support it),
		// write the file directly at the root.
		if writeErr := os.WriteFile(linkPath, data, 0644); writeErr != nil {
			return fmt.Errorf("creating symlink or writing config: symlink: %w, write: %w", err, writeErr)
		}
	}

	// Add opencode files to .git/info/exclude so they don't appear in git.
	addGitExcludes(directory, []string{"opencode.json", "opencode.jsonc", ".otto/opencode.json"})

	return nil
}

// addGitExcludes adds patterns to .git/info/exclude if not already present.
// Silently does nothing if the directory is not a git repo.
func addGitExcludes(repoDir string, patterns []string) {
	excludePath := filepath.Join(repoDir, ".git", "info", "exclude")

	// Read existing excludes.
	existing := make(map[string]bool)
	if f, err := os.Open(excludePath); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			existing[strings.TrimSpace(scanner.Text())] = true
		}
		f.Close()
	}

	// Collect patterns that need adding.
	var toAdd []string
	for _, p := range patterns {
		if !existing[p] {
			toAdd = append(toAdd, p)
		}
	}
	if len(toAdd) == 0 {
		return
	}

	// Ensure directory exists.
	_ = os.MkdirAll(filepath.Dir(excludePath), 0755)

	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return // not a git repo or permission issue — silently skip
	}
	defer f.Close()

	for _, p := range toAdd {
		fmt.Fprintf(f, "%s\n", p)
	}
}
