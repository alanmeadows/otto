package cli

import (
	"fmt"
	"time"

	"github.com/alanmeadows/otto/internal/opencode"
	"github.com/alanmeadows/otto/internal/provider"
	"github.com/alanmeadows/otto/internal/provider/ado"
	ghbackend "github.com/alanmeadows/otto/internal/provider/github"
	"github.com/alanmeadows/otto/internal/server"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var prCmd = &cobra.Command{
	Use:   "pr",
	Short: "Manage pull requests",
	Long: `Add, list, review, and manage the pull request lifecycle.

Otto tracks PRs across GitHub and Azure DevOps. Once a PR is added,
the daemon polls for review comments, auto-fixes issues via the LLM,
and pushes updated code — up to the configured max fix attempts.`,
	Example: `  otto pr add https://github.com/org/repo/pull/42
  otto pr list
  otto pr status
  otto pr review https://github.com/org/repo/pull/42`,
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

// buildRegistry creates a provider registry populated with backends from config.
func buildRegistry() *provider.Registry {
	reg := provider.NewRegistry()

	if appConfig != nil && appConfig.PR.Providers != nil {
		// Register ADO backend if configured.
		if adoCfg, ok := appConfig.PR.Providers["ado"]; ok {
			auth := ado.NewAuthProvider(adoCfg.PAT)
			adoBackend := ado.NewBackend(adoCfg.Organization, adoCfg.Project, auth)
			reg.Register(adoBackend)
		}

		// Register GitHub backend if configured.
		if ghCfg, ok := appConfig.PR.Providers["github"]; ok {
			ghBack := ghbackend.NewBackend("", "", ghCfg.Token)
			reg.Register(ghBack)
		}
	}

	return reg
}

var prAddCmd = &cobra.Command{
	Use:   "add <url>",
	Short: "Add a PR for tracking",
	Long: `Add a pull request URL for otto to track.

Otto detects the provider (GitHub or ADO) from the URL, fetches PR
metadata, and creates a local PR document. The daemon will begin
polling this PR for review feedback.`,
	Example: `  otto pr add https://github.com/org/repo/pull/42
  otto pr add https://dev.azure.com/org/project/_git/repo/pullrequest/123`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		prURL := args[0]

		// Detect backend from URL.
		reg := buildRegistry()
		backend, err := reg.Detect(prURL)
		if err != nil {
			return fmt.Errorf("detecting provider: %w", err)
		}

		// Fetch PR metadata.
		prInfo, err := backend.GetPR(ctx, prURL)
		if err != nil {
			return fmt.Errorf("fetching PR: %w", err)
		}

		// Create PR document.
		maxAttempts := appConfig.PR.MaxFixAttempts
		if maxAttempts <= 0 {
			maxAttempts = 5
		}

		pr := &server.PRDocument{
			ID:             prInfo.ID,
			Title:          prInfo.Title,
			Provider:       backend.Name(),
			Repo:           prInfo.RepoID,
			Branch:         prInfo.SourceBranch,
			Target:         prInfo.TargetBranch,
			Status:         "watching",
			URL:            prInfo.URL,
			Created:        time.Now().UTC().Format(time.RFC3339),
			LastChecked:    time.Now().UTC().Format(time.RFC3339),
			MaxFixAttempts: maxAttempts,
			Body:           fmt.Sprintf("# %s\n\n%s\n", prInfo.Title, prInfo.Description),
		}

		if err := server.SavePR(pr); err != nil {
			return fmt.Errorf("saving PR document: %w", err)
		}

		// TODO: POST to daemon API to start watching (Phase 8).

		fmt.Fprintf(cmd.OutOrStdout(), "Added PR #%s (%s) — %s\n", pr.ID, backend.Name(), prInfo.Title)
		return nil
	},
}

var prListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tracked PRs",
	Long: `Display all tracked pull requests in a table.

Shows PR ID, provider, status, branches, and fix attempt counts.`,
	Example: `  otto pr list`,
	RunE: func(cmd *cobra.Command, args []string) error {
		prs, err := server.ListPRs()
		if err != nil {
			return fmt.Errorf("listing PRs: %w", err)
		}

		if len(prs) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No tracked PRs. Add one with: otto pr add <url>")
			return nil
		}

		headerStyle := lipgloss.NewStyle().Bold(true).Padding(0, 1)
		cellStyle := lipgloss.NewStyle().Padding(0, 1)

		rows := make([][]string, 0, len(prs))
		for _, pr := range prs {
			rows = append(rows, []string{
				pr.ID,
				pr.Provider,
				pr.Status,
				pr.Branch,
				pr.Target,
				fmt.Sprintf("%d/%d", pr.FixAttempts, pr.MaxFixAttempts),
			})
		}

		t := table.New().
			Border(lipgloss.NormalBorder()).
			Headers("ID", "PROVIDER", "STATUS", "BRANCH", "TARGET", "FIXES").
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

var prStatusCmd = &cobra.Command{
	Use:   "status [id]",
	Short: "Show PR status",
	Long: `Show detailed status for a tracked pull request.

If no ID is given, otto infers the PR from the current branch.
Displays provider, status, branches, URL, and fix attempt count.`,
	Example: `  otto pr status
  otto pr status 42`,
	RunE: func(cmd *cobra.Command, args []string) error {
		var pr *server.PRDocument
		var err error

		if len(args) > 0 {
			pr, err = server.FindPR(args[0])
		} else {
			pr, err = server.InferPR()
		}
		if err != nil {
			return err
		}

		labelStyle := lipgloss.NewStyle().Bold(true)

		fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", labelStyle.Render("PR ID:"), pr.ID)
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", labelStyle.Render("Provider:"), pr.Provider)
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", labelStyle.Render("Status:"), pr.Status)
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", labelStyle.Render("Repo:"), pr.Repo)
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s → %s\n", labelStyle.Render("Branch:"), pr.Branch, pr.Target)
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", labelStyle.Render("URL:"), pr.URL)
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", labelStyle.Render("Created:"), pr.Created)
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", labelStyle.Render("Last Checked:"), pr.LastChecked)
		fmt.Fprintf(cmd.OutOrStdout(), "%s %d/%d\n", labelStyle.Render("Fix Attempts:"), pr.FixAttempts, pr.MaxFixAttempts)

		return nil
	},
}

var prRemoveCmd = &cobra.Command{
	Use:   "remove [id]",
	Short: "Remove a tracked PR",
	Long: `Stop tracking a pull request and delete its local document.

If no ID is given, otto infers the PR from the current branch.
This does not close the PR on the remote provider.`,
	Example: `  otto pr remove 42
  otto pr remove`,
	RunE: func(cmd *cobra.Command, args []string) error {
		var pr *server.PRDocument
		var err error

		if len(args) > 0 {
			pr, err = server.FindPR(args[0])
		} else {
			pr, err = server.InferPR()
		}
		if err != nil {
			return err
		}

		if err := server.DeletePR(pr.Provider, pr.ID); err != nil {
			return fmt.Errorf("removing PR: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Removed PR #%s (%s)\n", pr.ID, pr.Provider)
		return nil
	},
}

var prFixCmd = &cobra.Command{
	Use:   "fix [id]",
	Short: "Fix PR review issues",
	Long: `Attempt to fix outstanding review comments on a tracked PR.

Fetches unresolved review threads, sends them to the LLM for
resolution, and pushes the resulting changes. Increments the
fix attempt counter. If no ID is given, infers from current branch.`,
	Example: `  otto pr fix
  otto pr fix 42`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		var pr *server.PRDocument
		var err error

		if len(args) > 0 {
			pr, err = server.FindPR(args[0])
		} else {
			pr, err = server.InferPR()
		}
		if err != nil {
			return err
		}

		// Get backend for this PR.
		reg := buildRegistry()
		backend, err := reg.Get(pr.Provider)
		if err != nil {
			return fmt.Errorf("getting provider %q: %w", pr.Provider, err)
		}

		// Create server manager for OpenCode.
		mgr := opencode.NewServerManager(opencode.ServerManagerConfig{
			BaseURL:   appConfig.OpenCode.URL,
			AutoStart: appConfig.OpenCode.AutoStart,
			Password:  appConfig.OpenCode.Password,
			Username:  appConfig.OpenCode.Username,
		})
		if err := mgr.EnsureRunning(ctx); err != nil {
			return fmt.Errorf("starting OpenCode server: %w", err)
		}
		defer mgr.Shutdown()

		if err := server.FixPR(ctx, pr, backend, mgr, appConfig); err != nil {
			return fmt.Errorf("fixing PR: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "PR #%s fix attempt %d complete\n", pr.ID, pr.FixAttempts)
		return nil
	},
}

var prLogCmd = &cobra.Command{
	Use:   "log [id]",
	Short: "Show PR activity log",
	Long: `Display the activity log for a tracked pull request.

Shows the stored markdown body including review summaries, fix
attempts, and any notes. If no ID is given, infers from the
current branch.`,
	Example: `  otto pr log
  otto pr log 42`,
	RunE: func(cmd *cobra.Command, args []string) error {
		var pr *server.PRDocument
		var err error

		if len(args) > 0 {
			pr, err = server.FindPR(args[0])
		} else {
			pr, err = server.InferPR()
		}
		if err != nil {
			return err
		}

		if pr.Body == "" {
			fmt.Fprintln(cmd.OutOrStdout(), "No activity log for this PR.")
			return nil
		}

		fmt.Fprintln(cmd.OutOrStdout(), pr.Body)
		return nil
	},
}
