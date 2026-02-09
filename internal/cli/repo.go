package cli

import (
	"fmt"
	"os"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/repo"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var repoCmd = &cobra.Command{
	Use:   "repo",
	Short: "Manage repositories",
	Long: `Add, remove, and list tracked repositories.

Otto tracks repositories so it knows where to create worktrees,
which git strategy to use, and how to name branches. Use 'repo add'
to register a repository interactively and 'repo list' to inspect
the current set.`,
	Example: `  otto repo add my-service
  otto repo list
  otto repo remove my-service`,
}

func init() {
	repoCmd.AddCommand(repoAddCmd)
	repoCmd.AddCommand(repoRemoveCmd)
	repoCmd.AddCommand(repoListCmd)
}

var repoAddCmd = &cobra.Command{
	Use:   "add [name]",
	Short: "Add a repository",
	Long: `Register a repository for otto to manage.

Launches an interactive form to configure the repository name,
primary directory, worktree directory, git strategy (worktree,
branch, or hands-off), and branch naming template. If a name is
provided as an argument it is pre-filled in the form.`,
	Example: `  otto repo add
  otto repo add my-service`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, _ := os.Getwd()

		var name, primaryDir, worktreeDir, branchTemplate string
		var gitStrategy string

		// Seed defaults
		primaryDir = cwd
		branchTemplate = "otto/{{.Name}}"
		gitStrategy = "worktree"

		if len(args) > 0 {
			name = args[0]
		}

		// Run interactive huh form
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Repository name").
					Value(&name).
					Validate(func(s string) error {
						if s == "" {
							return fmt.Errorf("name is required")
						}
						return nil
					}),
				huh.NewInput().
					Title("Primary directory").
					Value(&primaryDir),
				huh.NewInput().
					Title("Worktree directory (leave empty for default)").
					Value(&worktreeDir),
				huh.NewSelect[string]().
					Title("Git strategy").
					Options(
						huh.NewOption("Worktree (recommended)", "worktree"),
						huh.NewOption("Branch", "branch"),
						huh.NewOption("Hands-off (read only)", "hands-off"),
					).
					Value(&gitStrategy),
				huh.NewInput().
					Title("Branch template").
					Value(&branchTemplate),
			),
		)

		if err := form.Run(); err != nil {
			return fmt.Errorf("form cancelled: %w", err)
		}

		repoCfg := config.RepoConfig{
			Name:           name,
			PrimaryDir:     primaryDir,
			WorktreeDir:    worktreeDir,
			GitStrategy:    config.GitStrategy(gitStrategy),
			BranchTemplate: branchTemplate,
		}

		configDir, err := os.UserConfigDir()
		if err != nil {
			return fmt.Errorf("getting config dir: %w", err)
		}

		mgr := repo.NewManager(configDir)
		if err := mgr.Add(appConfig, repoCfg); err != nil {
			return fmt.Errorf("adding repo: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Added repository %q (%s)\n", name, primaryDir)
		return nil
	},
}

var repoRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a repository",
	Long: `Remove a tracked repository by name.

This only removes the repository from otto's tracking configuration.
It does not delete the repository directory or any worktrees on disk.`,
	Example: `  otto repo remove my-service`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		configDir, err := os.UserConfigDir()
		if err != nil {
			return fmt.Errorf("getting config dir: %w", err)
		}

		mgr := repo.NewManager(configDir)
		if err := mgr.Remove(appConfig, name); err != nil {
			return fmt.Errorf("removing repo: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Removed repository %q\n", name)
		return nil
	},
}

var repoListCmd = &cobra.Command{
	Use:   "list",
	Short: "List repositories",
	Long: `Display all tracked repositories in a table.

Shows the repository name, primary directory, git strategy, and
branch template for each registered repository.`,
	Example: `  otto repo list`,
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return fmt.Errorf("getting config dir: %w", err)
		}

		mgr := repo.NewManager(configDir)
		repos := mgr.List(appConfig)

		if len(repos) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No repositories configured.")
			return nil
		}

		headerStyle := lipgloss.NewStyle().Bold(true).Padding(0, 1)
		cellStyle := lipgloss.NewStyle().Padding(0, 1)

		rows := make([][]string, 0, len(repos))
		for _, r := range repos {
			rows = append(rows, []string{r.Name, r.PrimaryDir, string(r.GitStrategy), r.BranchTemplate})
		}

		t := table.New().
			Border(lipgloss.NormalBorder()).
			Headers("NAME", "DIRECTORY", "STRATEGY", "BRANCH TEMPLATE").
			Rows(rows...).
			StyleFunc(func(row, col int) lipgloss.Style {
				if row == table.HeaderRow {
					return headerStyle
				}
				return cellStyle
			})

		fmt.Fprintln(cmd.OutOrStdout(), t)
		return nil
	},
}
