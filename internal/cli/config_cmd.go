package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/spf13/cobra"
	"github.com/tidwall/jsonc"
	"github.com/tidwall/sjson"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage otto configuration",
	Long:  `Show and modify otto configuration values.`,
}

var configJSONFlag bool

func init() {
	configShowCmd.Flags().BoolVar(&configJSONFlag, "json", false, "Output raw JSON without formatting")
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetCmd)
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show merged configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := appConfig
		if cfg == nil {
			var err error
			cfg, err = config.Load(configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
		}

		// Redact secrets before display.
		redacted := redactConfig(cfg)

		var data []byte
		var err error
		if configJSONFlag {
			data, err = json.Marshal(redacted)
		} else {
			data, err = json.MarshalIndent(redacted, "", "  ")
		}
		if err != nil {
			return fmt.Errorf("marshaling config: %w", err)
		}

		fmt.Fprintln(cmd.OutOrStdout(), string(data))
		return nil
	},
}

// redactConfig returns a copy of the config with secret fields masked.
func redactConfig(cfg *config.Config) *config.Config {
	copy := *cfg

	// Redact notification webhook URL.
	if copy.Notifications.TeamsWebhookURL != "" {
		copy.Notifications.TeamsWebhookURL = "***"
	}

	// Redact provider tokens/PATs.
	if copy.PR.Providers != nil {
		redacted := make(map[string]config.ProviderConfig, len(copy.PR.Providers))
		for k, v := range copy.PR.Providers {
			if v.PAT != "" {
				v.PAT = "***"
			}
			if v.Token != "" {
				v.Token = "***"
			}
			redacted[k] = v
		}
		copy.PR.Providers = redacted
	}

	// Redact OpenCode password.
	if copy.OpenCode.Password != "" {
		copy.OpenCode.Password = "***"
	}

	return &copy
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a config value",
	Long: `Set a configuration value using a dotted key path.

The value is written to .otto/otto.jsonc in the repository root.
The file is created if it does not exist.

Note: JSONC comments are not preserved on write.

Examples:
  otto config set models.primary "anthropic/claude-sonnet-4-20250514"
  otto config set server.port 8080
  otto config set opencode.auto_start true`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]
		rawValue := args[1]

		// Determine value type: try bool, then number, then string
		var value any
		if b, err := strconv.ParseBool(rawValue); err == nil {
			value = b
		} else if i, err := strconv.ParseInt(rawValue, 10, 64); err == nil {
			value = i
		} else if f, err := strconv.ParseFloat(rawValue, 64); err == nil {
			value = f
		} else {
			value = rawValue
		}

		repoRoot := config.RepoRoot()
		if repoRoot == "" {
			return fmt.Errorf("not in a git repository")
		}

		configDir := filepath.Join(repoRoot, ".otto")
		repoConfigPath := filepath.Join(configDir, "otto.jsonc")

		// Read existing file or start with empty JSON object
		var existing []byte
		if data, err := os.ReadFile(repoConfigPath); err == nil {
			// Strip JSONC comments before passing to sjson (which requires valid JSON).
			// Note: comments are not preserved on write.
			existing = jsonc.ToJSON(data)
		} else {
			existing = []byte("{}")
		}

		// Use sjson for in-place modification
		updated, err := sjson.SetBytes(existing, key, value)
		if err != nil {
			return fmt.Errorf("setting key %q: %w", key, err)
		}

		// Ensure directory exists
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return fmt.Errorf("creating config directory: %w", err)
		}

		if err := os.WriteFile(repoConfigPath, updated, 0644); err != nil {
			return fmt.Errorf("writing config: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Set %s = %v\n", key, value)
		return nil
	},
}
