package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var specTaskCmd = &cobra.Command{
	Use:   "task",
	Short: "Manage specification tasks",
	Long:  `Generate, list, add, and run tasks from a specification's design document.`,
}

func init() {
	specTaskCmd.AddCommand(specTaskGenerateCmd)
	specTaskCmd.AddCommand(specTaskListCmd)
	specTaskCmd.AddCommand(specTaskAddCmd)
	specTaskCmd.AddCommand(specTaskRunCmd)
}

var specTaskGenerateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate tasks from design",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var specTaskListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tasks and their status",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var specTaskAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a manual task",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var specTaskRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a specific task",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}
