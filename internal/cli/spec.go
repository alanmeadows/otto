package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var specCmd = &cobra.Command{
	Use:   "spec",
	Short: "Manage specifications",
	Long:  `Create, manage, and execute specifications for LLM-driven development workflows.`,
}

func init() {
	specCmd.AddCommand(specAddCmd)
	specCmd.AddCommand(specListCmd)
	specCmd.AddCommand(specRequirementsCmd)
	specCmd.AddCommand(specResearchCmd)
	specCmd.AddCommand(specDesignCmd)
	specCmd.AddCommand(specExecuteCmd)
	specCmd.AddCommand(specQuestionsCmd)
	specCmd.AddCommand(specRunCmd)
	specCmd.AddCommand(specTaskCmd)
}

var specAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new specification",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var specListCmd = &cobra.Command{
	Use:   "list",
	Short: "List specifications",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var specRequirementsCmd = &cobra.Command{
	Use:   "requirements",
	Short: "Generate/refine requirements document",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var specResearchCmd = &cobra.Command{
	Use:   "research",
	Short: "Generate/refine research document",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var specDesignCmd = &cobra.Command{
	Use:   "design",
	Short: "Generate/refine design document",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var specExecuteCmd = &cobra.Command{
	Use:   "execute",
	Short: "Execute tasks from specification",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var specQuestionsCmd = &cobra.Command{
	Use:   "questions",
	Short: "Manage spec questions",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}

var specRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run full specification pipeline",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
		return nil
	},
}
