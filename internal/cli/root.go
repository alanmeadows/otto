package cli

import (
	"github.com/alanmeadows/otto/internal/logging"
	"github.com/spf13/cobra"
)

var (
	verbose bool
	rootCmd = &cobra.Command{
		Use:   "otto",
		Short: "LLM-powered specification engine, task executor, and PR lifecycle manager",
		Long:  `Otto orchestrates LLM-driven workflows through OpenCode for specification authoring, task execution, and PR lifecycle management.`,
	}
)

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose/debug output")
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		logging.Setup(verbose)
	}
}

func Execute() error {
	return rootCmd.Execute()
}
