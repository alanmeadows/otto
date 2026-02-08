package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var worktreeCmd = &cobra.Command{
	Use:   "worktree",
	Short: "Manage git worktrees",
	Long:  `Create, list, and remove git worktrees for parallel development.`,
}

func init() {
	worktreeCmd.AddCommand(worktreeAddCmd)
	worktreeCmd.AddCommand(worktreeListCmd)
	worktreeCmd.AddCommand(worktreeRemoveCmd)
}

var worktreeAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Create a worktree",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var worktreeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List worktrees",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var worktreeRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a worktree",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}
