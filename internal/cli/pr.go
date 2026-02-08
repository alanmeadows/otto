package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var prCmd = &cobra.Command{
	Use:   "pr",
	Short: "Manage pull requests",
	Long:  `Add, list, review, and manage pull request lifecycle.`,
}

func init() {
	prCmd.AddCommand(prAddCmd)
	prCmd.AddCommand(prListCmd)
	prCmd.AddCommand(prStatusCmd)
	prCmd.AddCommand(prRemoveCmd)
	prCmd.AddCommand(prFixCmd)
	prCmd.AddCommand(prLogCmd)
	prCmd.AddCommand(prReviewCmd)
}

var prAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a PR for tracking",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var prListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tracked PRs",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var prStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show PR status",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var prRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove a tracked PR",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var prFixCmd = &cobra.Command{
	Use:   "fix",
	Short: "Fix PR review issues",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var prLogCmd = &cobra.Command{
	Use:   "log",
	Short: "Show PR activity log",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}
