package cli

import (
	"fmt"
	"os"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/logging"
	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags.
var Version = "dev"

var (
	verbose    bool
	configPath string
	appConfig  *config.Config
	rootCmd    = &cobra.Command{
		Use:   "otto",
		Short: "LLM-powered PR lifecycle manager",
		Long: `Otto orchestrates LLM-driven workflows through the GitHub Copilot SDK
for pull request lifecycle management.

It monitors PRs for review feedback, automatically fixes build failures,
resolves merge conflicts, and responds to code review comments — all
powered by LLM coding agents.

Run 'otto <command> --help' for details on any subcommand.`,
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
)

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose/debug output")
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "Path to config file override")

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		logging.Setup(verbose)
		cfg, err := config.Load(configPath)
		if err != nil {
			return err
		}
		appConfig = cfg
		return nil
	}

	// Wire all subcommands
	rootCmd.AddCommand(prCmd)
	rootCmd.AddCommand(repoCmd)
	rootCmd.AddCommand(worktreeCmd)
	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(configCmd)

	// Enable built-in shell completion command (bash, zsh, fish, powershell)
	rootCmd.CompletionOptions.DisableDefaultCmd = false
}

func Execute() error {
	err := rootCmd.Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
	}
	return err
}
