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
	Long: `Create, manage, and execute specifications for LLM-driven development workflows.

A specification starts as a natural-language prompt and progresses through
requirements, research, design, task generation, and execution phases.
Each phase produces a markdown document under .otto/specs/<slug>/.
Use the subcommands to drive each phase or 'spec run' for ad-hoc prompts.`,
	Example: `  otto spec add "Add retry logic to the HTTP client"
  otto spec list
  otto spec requirements --spec add-retry-logic
  otto spec execute --spec add-retry-logic`,
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
	specExecuteCmd.Flags().StringVar(&specSlugFlag, "spec", "", "Spec slug (optional if only one spec exists)")
	specQuestionsCmd.Flags().StringVar(&specSlugFlag, "spec", "", "Spec slug (optional if only one spec exists)")
	specRunCmd.Flags().StringVar(&specSlugFlag, "spec", "", "Spec slug (optional if only one spec exists)")
}

// specSlugFlag is shared by commands that accept --spec.
var specSlugFlag string

var specAddCmd = &cobra.Command{
	Use:   "add <prompt>",
	Short: "Add a new specification",
	Long: `Create a new specification from a natural-language prompt.

The LLM generates a slug and initial spec document under
.otto/specs/<slug>/. The prompt should clearly describe the
feature or change you want to implement.`,
	Example: `  otto spec add "Add retry logic to the HTTP client"
  otto spec add "Refactor database layer to use connection pooling"`,
	Args: cobra.ExactArgs(1),
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
	Long: `List all specifications in the current repository.

Displays each spec's slug and status. Specs are stored under
.otto/specs/ in the repository root.`,
	Example: `  otto spec list`,
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
	Long: `Generate or refine the requirements document for a specification.

The LLM analyzes the spec prompt and codebase to produce structured
requirements. Rerunning this command refines existing requirements.
Use --spec to target a specific spec when multiple exist.`,
	Example: `  otto spec requirements
  otto spec requirements --spec add-retry-logic`,
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
	Long: `Generate or refine the research document for a specification.

The LLM investigates the codebase and external context to produce
research notes that inform the design phase. Use --spec to target
a specific spec when multiple exist.`,
	Example: `  otto spec research
  otto spec research --spec add-retry-logic`,
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
	Long: `Generate or refine the design document for a specification.

Uses requirements and research to produce an implementation design.
The design document drives task generation. Use --spec to target
a specific spec when multiple exist.`,
	Example: `  otto spec design
  otto spec design --spec add-retry-logic`,
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
	Long: `Execute all pending tasks from a specification's task list.

Tasks are run in dependency order, respecting parallel groups.
Previously completed tasks are skipped. Use --spec to target
a specific spec when multiple exist.`,
	Example: `  otto spec execute
  otto spec execute --spec add-retry-logic`,
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
		return spec.Execute(cmd.Context(), client, appConfig, repoDir, slug)
	},
}

var specQuestionsCmd = &cobra.Command{
	Use:   "questions",
	Short: "Auto-resolve spec questions",
	Long: `Automatically resolve open questions in a specification.

The LLM examines the spec's requirements, research, and design
documents to answer outstanding questions collected during earlier
phases. Use --spec to target a specific spec when multiple exist.`,
	Example: `  otto spec questions
  otto spec questions --spec add-retry-logic`,
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
	Long: `Run an ad-hoc LLM prompt with the full spec context loaded.

This is useful for asking follow-up questions, requesting analysis,
or generating one-off artifacts without modifying spec documents.
Use --spec to target a specific spec when multiple exist.`,
	Example: `  otto spec run "Summarize the risks in this design"
  otto spec run --spec add-retry-logic "List all affected files"`,
	Args: cobra.ExactArgs(1),
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
