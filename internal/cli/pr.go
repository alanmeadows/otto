package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/alanmeadows/otto/internal/llm"
	"github.com/alanmeadows/otto/internal/prompts"
	"github.com/alanmeadows/otto/internal/provider"
	"github.com/alanmeadows/otto/internal/provider/ado"
	ghbackend "github.com/alanmeadows/otto/internal/provider/github"
	"github.com/alanmeadows/otto/internal/repo"
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
	prCmd.AddCommand(prSubmitCmd)
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

	// Always register GitHub as a fallback — it can auth via GITHUB_TOKEN
	// env var or gh CLI, and should match any github.com URL.
	if !reg.HasBackendFor("github.com") {
		token := os.Getenv("GITHUB_TOKEN")
		if token == "" {
			// Try gh CLI auth token.
			if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
				token = strings.TrimSpace(string(out))
			}
		}
		ghBack := ghbackend.NewBackend("", "", token)
		reg.Register(ghBack)
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
			waitingOn := pr.ComputeWaitingOn()

			// Build stages column: checkmarks for completed stages.
			var stages []string
			if pr.MerlinBotDone {
				stages = append(stages, "✓ merlinbot")
			} else {
				stages = append(stages, "○ merlinbot")
			}
			if pr.FeedbackDone {
				stages = append(stages, "✓ feedback")
			} else {
				stages = append(stages, "○ feedback")
			}
			switch pr.PipelineState {
			case "succeeded":
				stages = append(stages, "✓ pipelines")
			case "failed":
				stages = append(stages, "✗ pipelines")
			default:
				stages = append(stages, "○ pipelines")
			}
			stageStr := strings.Join(stages, " | ")

			rows = append(rows, []string{
				pr.ID,
				pr.Status,
				stageStr,
				waitingOn,
				pr.Branch,
				fmt.Sprintf("%d/%d", pr.FixAttempts, pr.MaxFixAttempts),
			})
		}

		t := table.New().
			Border(lipgloss.NormalBorder()).
			Headers("ID", "STATUS", "STAGES", "WAITING ON", "BRANCH", "FIXES").
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

		// Create LLM client.
		llmClient := llm.NewCopilotClient(appConfig.Models.Primary)
		if err := llmClient.Start(ctx); err != nil {
			return fmt.Errorf("starting Copilot LLM client: %w", err)
		}
		defer llmClient.Stop()

		if err := server.FixPR(ctx, pr, backend, llmClient, appConfig); err != nil {
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

var prSubmitCmd = &cobra.Command{
	Use:   "submit",
	Short: "Submit the current branch as a PR",
	Long: `Push the current branch and create a pull request on the configured provider.

This command is idempotent — if a PR already exists for the current branch,
it prints the existing PR URL and skips creation. After creating the PR,
otto optionally enables auto-complete, creates a linked work item, waits
for MerlinBot comments, evaluates and addresses them, then registers the
PR for monitoring.

Flags:
  --title     Override the PR title (default: LLM-generated from spec/commits)
  --target    Target branch (default: main)
  --no-monitor  Skip registering the PR for monitoring after creation`,
	Example: `  otto pr submit
  otto pr submit --title "Add widget support"
  otto pr submit --target develop`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		title, _ := cmd.Flags().GetString("title")
		target, _ := cmd.Flags().GetString("target")
		noMonitor, _ := cmd.Flags().GetBool("no-monitor")

		if target == "" {
			target = "main"
		}

		return submitPR(ctx, cmd, title, target, noMonitor)
	},
}

func init() {
	prSubmitCmd.Flags().String("title", "", "Override the PR title")
	prSubmitCmd.Flags().String("target", "main", "Target branch for the PR")
	prSubmitCmd.Flags().Bool("no-monitor", false, "Skip registering the PR for monitoring")
}

// submitPR is the main orchestrator for the pr submit workflow.
func submitPR(ctx context.Context, cmd *cobra.Command, titleOverride, targetBranch string, noMonitor bool) error {
	w := cmd.OutOrStdout()

	// Step 1: Detect current repo and branch.
	mgr := repo.NewManager("")
	repoCfg, err := mgr.FindByCWD(appConfig)
	if err != nil {
		return fmt.Errorf("detecting repository: %w\nRegister this repo with: otto repo add", err)
	}

	// Determine working directory — for worktree strategy, CWD is the worktree.
	workDir := repoCfg.PrimaryDir
	if repoCfg.GitStrategy == "worktree" {
		if cwd, err := os.Getwd(); err == nil {
			workDir = cwd
		}
	}

	// Get branch from the actual working directory (not the primary checkout).
	branchCmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	branchCmd.Dir = workDir
	branchOut, err := branchCmd.Output()
	if err != nil {
		return fmt.Errorf("getting current branch: %w", err)
	}
	branchName := strings.TrimSpace(string(branchOut))

	fmt.Fprintf(w, "Branch: %s → %s (repo: %s)\n", branchName, targetBranch, repoCfg.Name)

	dirty, err := repo.DirtyCheck(workDir)
	if err != nil {
		return fmt.Errorf("checking for uncommitted changes: %w", err)
	}
	if dirty {
		return fmt.Errorf("working directory has uncommitted changes — commit or stash first")
	}

	// Step 3: Get backend.
	providerName := appConfig.PR.DefaultProvider
	if providerName == "" {
		providerName = "ado"
	}
	reg := buildRegistry()
	backend, err := reg.Get(providerName)
	if err != nil {
		return fmt.Errorf("getting provider %q: %w", providerName, err)
	}

	// Set repository on backend if ADO.
	if adoBackend, ok := backend.(*ado.Backend); ok {
		repoName := repoNameFromRemote(workDir)
		if repoName != "" {
			adoBackend.SetRepository(repoName)
		}
	}

	// Step 4: Push branch.
	fmt.Fprintf(w, "Pushing branch to origin...\n")
	pushCmd := exec.CommandContext(ctx, "git", "push", "-u", "origin", branchName)
	pushCmd.Dir = workDir
	if out, err := pushCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push: %s: %w", strings.TrimSpace(string(out)), err)
	}
	fmt.Fprintf(w, "  ✓ Pushed\n")

	// Step 5: Idempotency check — look for existing PR.
	existingPR, err := backend.FindExistingPR(ctx, branchName)
	if err != nil && !errors.Is(err, provider.ErrUnsupported) {
		slog.Warn("failed to check for existing PR", "error", err)
	}
	if existingPR != nil {
		fmt.Fprintf(w, "PR already exists: %s\n", existingPR.URL)
		if !noMonitor {
			registerPRForMonitoring(w, existingPR, providerName)
		}
		return nil
	}

	// Step 6: Generate PR description via LLM.
	prTitle, prDescription, err := generatePRDescription(ctx, w, workDir, branchName, targetBranch, titleOverride)
	if err != nil {
		slog.Warn("failed to generate PR description, using fallback", "error", err)
		if titleOverride != "" {
			prTitle = titleOverride
		} else {
			prTitle = branchName
		}
		prDescription = fmt.Sprintf("Automated PR for branch %s", branchName)
	}

	fmt.Fprintf(w, "PR Title: %s\n", prTitle)

	// Step 7: Create PR.
	fmt.Fprintf(w, "Creating PR...\n")
	prInfo, err := backend.CreatePR(ctx, provider.CreatePRParams{
		Title:        prTitle,
		Description:  prDescription,
		SourceBranch: branchName,
		TargetBranch: targetBranch,
	})
	if err != nil {
		return fmt.Errorf("creating PR: %w", err)
	}
	fmt.Fprintf(w, "  ✓ Created PR #%s: %s\n", prInfo.ID, prInfo.URL)

	// Step 8: Auto-complete (if configured).
	if adoCfg, ok := appConfig.PR.Providers[providerName]; ok && adoCfg.AutoComplete {
		fmt.Fprintf(w, "Enabling auto-complete...\n")
		if err := backend.RunWorkflow(ctx, prInfo, provider.WorkflowAutoComplete); err != nil {
			slog.Warn("failed to enable auto-complete", "error", err)
			fmt.Fprintf(w, "  ⚠ Auto-complete failed: %v\n", err)
		} else {
			fmt.Fprintf(w, "  ✓ Auto-complete enabled\n")
		}
	}

	// Step 9: Trigger work item creation via copilot comment (if configured).
	var workItemThreadID string
	if adoCfg, ok := appConfig.PR.Providers[providerName]; ok && adoCfg.CreateWorkItem {
		adoBackend, ok := backend.(*ado.Backend)
		if !ok {
			slog.Warn("work item trigger only supported for ADO backend")
		} else {
			areaPath := adoCfg.WorkItemAreaPath
			if areaPath == "" {
				areaPath = `One\AzureStack\ASZ-VM self service`
			}
			triggerBody := fmt.Sprintf("copilot: generateworkitem | areapath: %s", areaPath)
			fmt.Fprintf(w, "Posting work item trigger comment...\n")
			threadID, err := adoBackend.PostCommentThread(ctx, prInfo, triggerBody)
			if err != nil {
				slog.Warn("failed to post work item trigger comment", "error", err)
				fmt.Fprintf(w, "  ⚠ Work item trigger failed: %v\n", err)
			} else {
				workItemThreadID = threadID
				fmt.Fprintf(w, "  ✓ Work item trigger posted (thread %s)\n", threadID)
			}
		}
	}

	// Close the work item trigger thread.
	if workItemThreadID != "" {
		if adoBackend, ok := backend.(*ado.Backend); ok {
			if err := adoBackend.UpdateThreadStatus(ctx, prInfo, workItemThreadID, "closed"); err != nil {
				slog.Warn("failed to close work item trigger thread", "threadID", workItemThreadID, "error", err)
			} else {
				fmt.Fprintf(w, "  ✓ Closed work item trigger thread\n")
			}
		}
	}

	// Step 10: Register for monitoring — daemon handles MerlinBot asynchronously.
	if !noMonitor {
		registerPRForMonitoring(w, prInfo, providerName)
	}

	fmt.Fprintf(w, "\nDone! PR #%s: %s\n", prInfo.ID, prInfo.URL)
	return nil
}

// generatePRDescription uses the LLM to generate a PR title and description.
func generatePRDescription(ctx context.Context, w io.Writer, workDir, branchName, targetBranch, titleOverride string) (string, string, error) {
	// Gather commit log.
	logCmd := exec.CommandContext(ctx, "git", "log", fmt.Sprintf("origin/%s..HEAD", targetBranch), "--oneline", "--no-decorate")
	logCmd.Dir = workDir
	logOut, err := logCmd.Output()
	if err != nil {
		// Fallback: use all recent commits.
		logCmd = exec.CommandContext(ctx, "git", "log", "-20", "--oneline", "--no-decorate")
		logCmd.Dir = workDir
		logOut, _ = logCmd.Output()
	}
	commitLog := strings.TrimSpace(string(logOut))
	if commitLog == "" {
		commitLog = "(no commits)"
	}

	// Build prompt.
	templateData := map[string]string{
		"BranchName": branchName,
		"CommitLog":  commitLog,
	}

	prompt, err := prompts.Execute("pr-description.md", templateData)
	if err != nil {
		return "", "", fmt.Errorf("building PR description prompt: %w", err)
	}

	// Create LLM client.
	llmClient := llm.NewCopilotClient(appConfig.Models.Primary)
	if err := llmClient.Start(ctx); err != nil {
		return "", "", fmt.Errorf("starting Copilot LLM client: %w", err)
	}
	defer llmClient.Stop()

	session, err := llmClient.CreateSession(ctx, "PR Description", workDir)
	if err != nil {
		return "", "", fmt.Errorf("creating session: %w", err)
	}
	defer llmClient.DeleteSession(ctx, session.ID)

	resp, err := llmClient.SendPrompt(ctx, session.ID, prompt)
	if err != nil {
		return "", "", fmt.Errorf("LLM prompt failed: %w", err)
	}

	description := strings.TrimSpace(resp.Content)

	// Extract title: use override, or first line of description, or branch name.
	title := titleOverride
	if title == "" {
		title = extractPRTitle(description, branchName)
		// Remove the title line from the description.
		lines := strings.SplitN(description, "\n", 2)
		if len(lines) > 1 {
			description = strings.TrimSpace(lines[1])
		}
	}

	return title, description, nil
}

// extractPRTitle pulls a clean title from the LLM response, falling back to
// the branch name if the LLM produced conversational preamble instead.
func extractPRTitle(description, branchName string) string {
	lines := strings.SplitN(description, "\n", 2)
	title := strings.TrimSpace(lines[0])

	// Strip markdown heading prefixes (# , ## , etc.).
	for strings.HasPrefix(title, "#") {
		title = strings.TrimPrefix(title, "#")
	}
	title = strings.TrimSpace(title)

	// Reject titles that look like conversational LLM preamble.
	conversationalPrefixes := []string{
		"now ", "here ", "i ", "let me", "sure", "okay", "based on",
		"looking at", "after reviewing", "i've", "i'll", "this pr",
	}
	lower := strings.ToLower(title)
	for _, prefix := range conversationalPrefixes {
		if strings.HasPrefix(lower, prefix) {
			slog.Warn("LLM generated conversational PR title, using branch name", "badTitle", title)
			return branchName
		}
	}

	// Truncate excessively long titles.
	if len(title) > 100 {
		title = title[:97] + "..."
	}

	// If empty, fall back to branch name.
	if title == "" {
		return branchName
	}

	return title
}

// registerPRForMonitoring saves the PR as a tracking document for the monitoring loop.
// MerlinBot handling is always deferred to the daemon.
func registerPRForMonitoring(w io.Writer, prInfo *provider.PRInfo, providerName string) {
	maxAttempts := 5
	if appConfig != nil && appConfig.PR.MaxFixAttempts > 0 {
		maxAttempts = appConfig.PR.MaxFixAttempts
	}

	pr := &server.PRDocument{
		ID:             prInfo.ID,
		Title:          prInfo.Title,
		Provider:       providerName,
		Repo:           prInfo.RepoID,
		Branch:         prInfo.SourceBranch,
		Target:         prInfo.TargetBranch,
		Status:         "watching",
		URL:            prInfo.URL,
		Created:        time.Now().UTC().Format(time.RFC3339),
		LastChecked:    time.Now().UTC().Format(time.RFC3339),
		MaxFixAttempts: maxAttempts,
		PipelineState:  "pending",
		Body:           fmt.Sprintf("# %s\n\n%s\n", prInfo.Title, prInfo.Description),
	}

	if err := server.SavePR(pr); err != nil {
		fmt.Fprintf(w, "  ⚠ Failed to register PR for monitoring: %v\n", err)
	} else {
		fmt.Fprintf(w, "  ✓ Registered PR for monitoring\n")
		// Signal the daemon to immediately poll the new PR.
		notifyDaemon(w)
	}
}

// notifyDaemon sends a POST /poll to the running daemon to trigger an immediate poll cycle.
// Failures are non-fatal — the daemon will pick up the PR on the next regular cycle.
func notifyDaemon(w io.Writer) {
	port := 4097
	if appConfig != nil && appConfig.Server.Port > 0 {
		port = appConfig.Server.Port
	}
	resp, err := http.Post(fmt.Sprintf("http://localhost:%d/poll", port), "application/json", nil)
	if err != nil {
		slog.Debug("could not notify daemon for immediate poll", "error", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		fmt.Fprintf(w, "  ✓ Daemon notified — immediate poll triggered\n")
	}
}

// repoNameFromRemote extracts the repository name from the git remote URL.
func repoNameFromRemote(workDir string) string {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	remote := strings.TrimSpace(string(out))

	// Extract repo name from URL patterns:
	// https://dev.azure.com/org/project/_git/RepoName
	// https://org.visualstudio.com/project/_git/RepoName
	// git@ssh.dev.azure.com:v3/org/project/RepoName
	parts := strings.Split(remote, "/")
	if len(parts) > 0 {
		name := parts[len(parts)-1]
		name = strings.TrimSuffix(name, ".git")
		return name
	}
	return ""
}
