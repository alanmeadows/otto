package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dario.cat/mergo"
	"github.com/tidwall/jsonc"
)

// Load reads and merges configuration from user-level and repo-level JSONC files.
// Resolution order: user config (~/.config/otto/otto.jsonc) → deep-merged with repo config (.otto/otto.jsonc).
func Load() (*Config, error) {
	cfg := DefaultConfig()

	// Load user-level config
	userDir, err := os.UserConfigDir()
	if err == nil {
		userPath := filepath.Join(userDir, "otto", "otto.jsonc")
		if userMap, err := loadJSONC(userPath); err == nil {
			if err := mergeIntoConfig(&cfg, userMap); err != nil {
				return nil, fmt.Errorf("merging user config: %w", err)
			}
		}
	}

	// Load repo-level config
	repoRoot := findRepoRoot()
	if repoRoot != "" {
		repoPath := filepath.Join(repoRoot, ".otto", "otto.jsonc")
		if repoMap, err := loadJSONC(repoPath); err == nil {
			if err := mergeIntoConfig(&cfg, repoMap); err != nil {
				return nil, fmt.Errorf("merging repo config: %w", err)
			}
		}
	}

	// Environment variable overrides
	applyEnvOverrides(&cfg)

	return &cfg, nil
}

// loadJSONC reads a JSONC file and returns it as a map.
func loadJSONC(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	jsonData := jsonc.ToJSON(data)
	var m map[string]any
	if err := json.Unmarshal(jsonData, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return m, nil
}

// mergeIntoConfig marshals the config to a map, deep-merges the source map over it,
// then unmarshals back to the Config struct.
func mergeIntoConfig(cfg *Config, src map[string]any) error {
	// Marshal current config to map
	cfgBytes, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	var dst map[string]any
	if err := json.Unmarshal(cfgBytes, &dst); err != nil {
		return err
	}

	// Deep merge: src overrides dst
	if err := mergo.Merge(&dst, src, mergo.WithOverride); err != nil {
		return err
	}

	// Unmarshal merged map back to Config
	merged, err := json.Marshal(dst)
	if err != nil {
		return err
	}
	return json.Unmarshal(merged, cfg)
}

// findRepoRoot finds the git repository root via git rev-parse.
func findRepoRoot() string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// applyEnvOverrides applies environment variable overrides to the config.
func applyEnvOverrides(cfg *Config) {
	if pat := os.Getenv("OTTO_ADO_PAT"); pat != "" {
		if cfg.PR.Providers == nil {
			cfg.PR.Providers = make(map[string]ProviderConfig)
		}
		ado := cfg.PR.Providers["ado"]
		ado.PAT = pat
		cfg.PR.Providers["ado"] = ado
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		if cfg.PR.Providers == nil {
			cfg.PR.Providers = make(map[string]ProviderConfig)
		}
		gh := cfg.PR.Providers["github"]
		gh.Token = token
		cfg.PR.Providers["github"] = gh
	}
	if pw := os.Getenv("OPENCODE_SERVER_PASSWORD"); pw != "" {
		cfg.OpenCode.Password = pw
	}
	if user := os.Getenv("OPENCODE_SERVER_USERNAME"); user != "" {
		cfg.OpenCode.Username = user
	}
}

// RepoRoot returns the detected git repository root, or empty string if not in a repo.
func RepoRoot() string {
	return findRepoRoot()
}
