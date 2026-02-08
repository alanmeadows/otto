package cli

import (
	"fmt"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/spec"
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

	// Add --spec flags
	specRequirementsCmd.Flags().StringVar(&specSlugFlag, "spec", "", "Spec slug (optional if only one spec exists)")
	specResearchCmd.Flags().StringVar(&specSlugFlag, "spec", "", "Spec slug (optional if only one spec exists)")
	specDesignCmd.Flags().StringVar(&specSlugFlag, "spec", "", "Spec slug (optional if only one spec exists)")
	specQuestionsCmd.Flags().StringVar(&specSlugFlag, "spec", "", "Spec slug (optional if only one spec exists)")
	specRunCmd.Flags().StringVar(&specSlugFlag, "spec", "", "Spec slug (optional if only one spec exists)")
}

// specSlugFlag is shared by commands that accept --spec.
var specSlugFlag string

var specAddCmd = &cobra.Command{
	Use:   "add <prompt>",
	Short: "Add a new specification",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, mgr, err := newLLMClient(appConfig)
		if err != nil {
			return err
		}
		defer mgr.Shutdown()

		repoDir := config.RepoRoot()
		if repoDir == "" {
			return fmt.Errorf("not in a git repository")
		}

		s, err := spec.SpecAdd(cmd.Context(), client, appConfig, repoDir, args[0])
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Created spec: %s\nPath: %s\n", s.Slug, s.Dir)
		return nil
	},
}

var specListCmd = &cobra.Command{
	Use:   "list",
	Short: "List specifications",
	RunE: func(cmd *cobra.Command, args []string) error {
		repoDir := config.RepoRoot()
		if repoDir == "" {
			return fmt.Errorf("not in a git repository")
		}

		return spec.SpecList(cmd.OutOrStdout(), repoDir)
	},
}

var specRequirementsCmd = &cobra.Command{
	Use:   "requirements",
	Short: "Generate/refine requirements document",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, mgr, err := newLLMClient(appConfig)
		if err != nil {
			return err
		}
		defer mgr.Shutdown()

		repoDir := config.RepoRoot()
		if repoDir == "" {
			return fmt.Errorf("not in a git repository")
		}

		slug, _ := cmd.Flags().GetString("spec")
		if err := spec.SpecRequirements(cmd.Context(), client, appConfig, repoDir, slug); err != nil {
			return err
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Requirements updated.")
		return nil
	},
}

var specResearchCmd = &cobra.Command{
	Use:   "research",
	Short: "Generate/refine research document",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, mgr, err := newLLMClient(appConfig)
		if err != nil {
			return err
		}
		defer mgr.Shutdown()

		repoDir := config.RepoRoot()
		if repoDir == "" {
			return fmt.Errorf("not in a git repository")
		}

		slug, _ := cmd.Flags().GetString("spec")
		if err := spec.SpecResearch(cmd.Context(), client, appConfig, repoDir, slug); err != nil {
			return err
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Research updated.")
		return nil
	},
}

var specDesignCmd = &cobra.Command{
	Use:   "design",
	Short: "Generate/refine design document",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, mgr, err := newLLMClient(appConfig)
		if err != nil {
			return err
		}
		defer mgr.Shutdown()

		repoDir := config.RepoRoot()
		if repoDir == "" {
			return fmt.Errorf("not in a git repository")
		}

		slug, _ := cmd.Flags().GetString("spec")
		if err := spec.SpecDesign(cmd.Context(), client, appConfig, repoDir, slug); err != nil {
			return err
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Design updated.")
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
	Short: "Auto-resolve spec questions",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, mgr, err := newLLMClient(appConfig)
		if err != nil {
			return err
		}
		defer mgr.Shutdown()

		repoDir := config.RepoRoot()
		if repoDir == "" {
			return fmt.Errorf("not in a git repository")
		}

		slug, _ := cmd.Flags().GetString("spec")
		if err := spec.SpecQuestions(cmd.Context(), client, appConfig, repoDir, slug); err != nil {
			return err
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Question resolution complete.")
		return nil
	},
}

var specRunCmd = &cobra.Command{
	Use:   "run <prompt>",
	Short: "Run ad-hoc prompt against spec context",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, mgr, err := newLLMClient(appConfig)
		if err != nil {
			return err
		}
		defer mgr.Shutdown()

		repoDir := config.RepoRoot()
		if repoDir == "" {
			return fmt.Errorf("not in a git repository")
		}

		slug, _ := cmd.Flags().GetString("spec")
		result, err := spec.SpecRun(cmd.Context(), client, appConfig, repoDir, slug, args[0])
		if err != nil {
			return err
		}

		fmt.Fprintln(cmd.OutOrStdout(), result)
		return nil
	},
}
