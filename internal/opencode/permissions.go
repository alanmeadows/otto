package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// EnsurePermissions writes the OpenCode permission config to the target directory.
// This ensures OpenCode runs in "yolo mode" for automated operation.
// The file is written as opencode.json in the specified directory.
// Idempotent: overwrites any existing file.
func EnsurePermissions(directory string) error {
	cfg := DefaultPermissions()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling permission config: %w", err)
	}

	configPath := filepath.Join(directory, "opencode.json")
	if err := os.MkdirAll(directory, 0755); err != nil {
		return fmt.Errorf("creating directory %s: %w", directory, err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("writing permission config to %s: %w", configPath, err)
	}

	return nil
}
