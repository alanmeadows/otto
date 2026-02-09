package cli

import (
	"fmt"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/spec"
	"github.com/spf13/cobra"
)

var specTaskCmd = &cobra.Command{
	Use:   "task",
	Short: "Manage specification tasks",
	Long:  `Generate, list, add, and run tasks from a specification's design document.`,
}

// taskSpecSlugFlag is the --spec flag for task subcommands.
var taskSpecSlugFlag string

// taskIDFlag is the --id flag for the run subcommand.
var taskIDFlag string

func init() {
	specTaskCmd.AddCommand(specTaskGenerateCmd)
	specTaskCmd.AddCommand(specTaskListCmd)
	specTaskCmd.AddCommand(specTaskAddCmd)
	specTaskCmd.AddCommand(specTaskRunCmd)

	specTaskGenerateCmd.Flags().StringVar(&taskSpecSlugFlag, "spec", "", "Spec slug (optional if only one spec exists)")
	specTaskListCmd.Flags().StringVar(&taskSpecSlugFlag, "spec", "", "Spec slug (optional if only one spec exists)")
	specTaskAddCmd.Flags().StringVar(&taskSpecSlugFlag, "spec", "", "Spec slug (optional if only one spec exists)")
	specTaskRunCmd.Flags().StringVar(&taskSpecSlugFlag, "spec", "", "Spec slug (optional if only one spec exists)")
	specTaskRunCmd.Flags().StringVar(&taskIDFlag, "id", "", "Task ID to run (inferred if only one runnable)")
}

var specTaskGenerateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate tasks from design",
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
		if err := spec.SpecTaskGenerate(cmd.Context(), client, appConfig, repoDir, slug); err != nil {
			return err
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Tasks generated.")
		return nil
	},
}

var specTaskListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tasks and their status",
	RunE: func(cmd *cobra.Command, args []string) error {
		repoDir := config.RepoRoot()
		if repoDir == "" {
			return fmt.Errorf("not in a git repository")
		}

		slug, _ := cmd.Flags().GetString("spec")
		s, err := spec.ResolveSpec(slug, repoDir)
		if err != nil {
			return err
		}

		if !s.HasTasks() {
			fmt.Fprintln(cmd.OutOrStdout(), "No tasks found. Generate with: otto spec task generate")
			return nil
		}

		tasks, err := spec.ParseTasks(s.TasksPath)
		if err != nil {
			return fmt.Errorf("parsing tasks: %w", err)
		}

		if len(tasks) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No tasks found in tasks.md.")
			return nil
		}

		for _, t := range tasks {
			statusIcon := statusToIcon(t.Status)
			fmt.Fprintf(cmd.OutOrStdout(), "  %s %s [%s] (group %d) %s\n",
				statusIcon, t.ID, t.Status, t.ParallelGroup, t.Title)
		}

		// Show runnable tasks
		runnable := spec.GetRunnableTasks(tasks)
		if len(runnable) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "\nRunnable tasks: ")
			for i, t := range runnable {
				if i > 0 {
					fmt.Fprint(cmd.OutOrStdout(), ", ")
				}
				fmt.Fprint(cmd.OutOrStdout(), t.ID)
			}
			fmt.Fprintln(cmd.OutOrStdout())
		}

		return nil
	},
}

var specTaskAddCmd = &cobra.Command{
	Use:   "add <prompt>",
	Short: "Add a manual task",
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
		if err := spec.SpecTaskAdd(cmd.Context(), client, appConfig, repoDir, slug, args[0]); err != nil {
			return err
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Task added.")
		return nil
	},
}

var specTaskRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a specific task",
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
		taskID, _ := cmd.Flags().GetString("id")
		if err := spec.RunTask(cmd.Context(), client, appConfig, repoDir, slug, taskID, ""); err != nil {
			return err
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Task completed.")
		return nil
	},
}

func statusToIcon(status string) string {
	switch status {
	case spec.TaskStatusCompleted:
		return "✓"
	case spec.TaskStatusRunning:
		return "▶"
	case spec.TaskStatusFailed:
		return "✗"
	case spec.TaskStatusSkipped:
		return "⊘"
	default:
		return "○"
	}
}
