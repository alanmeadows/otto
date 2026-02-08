package cli

import (
	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/logging"
	"github.com/spf13/cobra"
)

var (
	verbose    bool
	configPath string
	appConfig  *config.Config
	rootCmd    = &cobra.Command{
		Use:   "otto",
		Short: "LLM-powered specification engine, task executor, and PR lifecycle manager",
		Long:  `Otto orchestrates LLM-driven workflows through OpenCode for specification authoring, task execution, and PR lifecycle management.`,
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
	rootCmd.AddCommand(specCmd)
	rootCmd.AddCommand(prCmd)
	rootCmd.AddCommand(repoCmd)
	rootCmd.AddCommand(worktreeCmd)
	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(configCmd)
}

func Execute() error {
	return rootCmd.Execute()
}
