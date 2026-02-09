package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var worktreeCmd = &cobra.Command{
	Use:   "worktree",
	Short: "Manage git worktrees",
	Long: `Create, list, and remove git worktrees for parallel development.

Worktrees let otto work on multiple specs or PRs simultaneously
without switching branches in the primary checkout. Each worktree
gets its own working directory and branch.`,
	Example: `  otto worktree add feature-x
  otto worktree list
  otto worktree remove feature-x`,
}

func init() {
	worktreeCmd.AddCommand(worktreeAddCmd)
	worktreeCmd.AddCommand(worktreeListCmd)
	worktreeCmd.AddCommand(worktreeRemoveCmd)
}

var worktreeAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Create a worktree",
	Long: `Create a new git worktree with the given name.

A branch is created using the repository's branch template and
the worktree is checked out in the configured worktree directory.`,
	Example: `  otto worktree add feature-x`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var worktreeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List worktrees",
	Long: `List all git worktrees managed by otto.

Displays the worktree name, branch, and directory path.`,
	Example: `  otto worktree list`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var worktreeRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a worktree",
	Long: `Remove a git worktree by name.

Deletes the worktree directory and prunes the git worktree entry.
The associated branch is not deleted automatically.`,
	Example: `  otto worktree remove feature-x`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}
