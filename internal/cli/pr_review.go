package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var prReviewCmd = &cobra.Command{
	Use:   "review <url>",
	Short: "Review a pull request",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}
