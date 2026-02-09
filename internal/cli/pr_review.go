package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"

	"github.com/alanmeadows/otto/internal/opencode"
	"github.com/alanmeadows/otto/internal/prompts"
	"github.com/alanmeadows/otto/internal/provider"
	"github.com/alanmeadows/otto/internal/repo"
	"github.com/alanmeadows/otto/internal/spec"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

// reviewComment represents a single review comment parsed from LLM output.
type reviewComment struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Body     string `json:"body"`
}

var prReviewCmd = &cobra.Command{
	Use:   "review <url>",
	Short: "Review a pull request",
	Long: `Perform an LLM-powered code review on a pull request.

Otto clones/checks-out the PR branch, sends the diff and codebase
context to the LLM, and presents the resulting review comments in a
table. You then interactively select which comments to post as
inline review comments on the PR. A summary comment is posted
automatically.`,
	Example: `  otto pr review https://github.com/org/repo/pull/42
  otto pr review https://dev.azure.com/org/project/_git/repo/pullrequest/123`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		prURL := args[0]

		// Step 1: Detect backend from URL.
		reg := buildRegistry()
		backend, err := reg.Detect(prURL)
		if err != nil {
			return fmt.Errorf("detecting provider: %w", err)
		}

		// Step 2: Fetch PR metadata.
		prInfo, err := backend.GetPR(ctx, prURL)
		if err != nil {
			return fmt.Errorf("fetching PR: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Reviewing PR #%s: %s\n", prInfo.ID, prInfo.Title)

		// Step 3: Map to local repo — fetch + checkout source branch.
		workDir, cleanup, err := repo.MapPRToWorkDir(appConfig, prInfo.URL, prInfo.SourceBranch)
		if err != nil {
			return fmt.Errorf("mapping PR to workdir: %w", err)
		}
		if cleanup != nil {
			defer cleanup()
		}

		// Analyze codebase for review context.
		summary, err := spec.AnalyzeCodebase(workDir)
		if err != nil {
			slog.Warn("codebase analysis failed, continuing without summary", "error", err)
		}

		// Step 4: Ensure OpenCode permissions.
		if err := opencode.EnsurePermissions(workDir); err != nil {
			return fmt.Errorf("ensuring permissions: %w", err)
		}

		// Step 5: Create OpenCode session.
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

		llm := mgr.LLM()
		if llm == nil {
			return fmt.Errorf("OpenCode server started but no LLM client available")
		}

		model := opencode.ParseModelRef(appConfig.Models.Primary)

		// Step 6: Send pr-review.md prompt.
		templateData := map[string]string{
			"pr_title":       prInfo.Title,
			"pr_description": prInfo.Description,
			"target_branch":  prInfo.TargetBranch,
		}
		if summary != nil {
			templateData["codebase_summary"] = summary.String()
		}

		prompt, err := prompts.Execute("pr-review.md", templateData)
		if err != nil {
			return fmt.Errorf("building review prompt: %w", err)
		}

		session, err := llm.CreateSession(ctx, fmt.Sprintf("PR Review #%s", prInfo.ID), workDir)
		if err != nil {
			return fmt.Errorf("creating review session: %w", err)
		}
		defer llm.DeleteSession(ctx, session.ID, workDir)

		fmt.Fprintf(cmd.OutOrStdout(), "Analyzing changes against %s...\n", prInfo.TargetBranch)

		resp, err := llm.SendPrompt(ctx, session.ID, prompt, model, workDir)
		if err != nil {
			return fmt.Errorf("review prompt failed: %w", err)
		}

		// Step 7: Parse JSON response.
		comments, err := opencode.ParseJSONResponse[[]reviewComment](ctx, llm, session.ID, workDir, model, resp.Content)
		if err != nil {
			return fmt.Errorf("parsing review response: %w", err)
		}

		if len(comments) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No issues found — the PR looks clean.")
			return nil
		}

		// Step 8: Present comments in table.
		fmt.Fprintf(cmd.OutOrStdout(), "\nFound %d review comments:\n\n", len(comments))

		headerStyle := lipgloss.NewStyle().Bold(true).Padding(0, 1)
		cellStyle := lipgloss.NewStyle().Padding(0, 1)

		rows := make([][]string, 0, len(comments))
		for i, c := range comments {
			// Truncate body for table display.
			body := c.Body
			if len(body) > 80 {
				body = body[:77] + "..."
			}
			rows = append(rows, []string{
				strconv.Itoa(i + 1),
				c.Severity,
				c.File,
				strconv.Itoa(c.Line),
				body,
			})
		}

		t := table.New().
			Border(lipgloss.NormalBorder()).
			Headers("#", "SEVERITY", "FILE", "LINE", "COMMENT").
			Rows(rows...).
			StyleFunc(func(row, col int) lipgloss.Style {
				if row == table.HeaderRow {
					return headerStyle
				}
				return cellStyle
			})

		fmt.Fprintln(cmd.OutOrStdout(), t)

		// Step 9: Interactive approval via huh.NewMultiSelect.
		options := make([]huh.Option[int], 0, len(comments))
		for i, c := range comments {
			label := fmt.Sprintf("[%s] %s:%d — %s", c.Severity, c.File, c.Line, truncateStr(c.Body, 60))
			options = append(options, huh.NewOption(label, i).Selected(true))
		}

		var selected []int
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewMultiSelect[int]().
					Title("Select comments to post").
					Options(options...).
					Value(&selected),
			),
		)

		if err := form.Run(); err != nil {
			return fmt.Errorf("selection cancelled: %w", err)
		}

		if len(selected) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No comments selected — skipping.")
			return nil
		}

		// Step 10: Post approved comments via backend.PostInlineComment().
		fmt.Fprintf(cmd.OutOrStdout(), "Posting %d comments...\n", len(selected))
		posted, err := postReviewComments(ctx, cmd.OutOrStdout(), backend, prInfo, comments, selected)
		if err != nil {
			return fmt.Errorf("posting comments: %w", err)
		}

		// Step 11: Post optional summary comment.
		reviewSummary := buildReviewSummary(comments, selected)
		if err := backend.PostComment(ctx, prInfo, reviewSummary); err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Warning: failed to post summary comment: %v\n", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Posted %d comments to PR #%s\n", posted, prInfo.ID)
		return nil
	},
}

// postReviewComments posts the selected inline comments to the PR.
func postReviewComments(ctx context.Context, w io.Writer, backend provider.PRBackend, prInfo *provider.PRInfo, comments []reviewComment, selected []int) (int, error) {
	posted := 0
	for _, idx := range selected {
		c := comments[idx]
		inline := provider.InlineComment{
			FilePath: c.File,
			Line:     c.Line,
			Body:     fmt.Sprintf("**[%s]** %s", strings.ToUpper(c.Severity), c.Body),
			Side:     "right",
		}
		if err := backend.PostInlineComment(ctx, prInfo, inline); err != nil {
			fmt.Fprintf(w, "  Warning: failed to post comment on %s:%d: %v\n", c.File, c.Line, err)
			continue
		}
		posted++
	}
	return posted, nil
}

// buildReviewSummary creates a markdown summary of the review.
func buildReviewSummary(comments []reviewComment, selected []int) string {
	var errors, warnings, nitpicks, other int
	for _, idx := range selected {
		switch strings.ToLower(comments[idx].Severity) {
		case "error", "critical":
			errors++
		case "warning":
			warnings++
		case "nitpick", "nit", "info":
			nitpicks++
		default:
			other++
		}
	}

	var sb strings.Builder
	sb.WriteString("## otto review summary\n\n")
	sb.WriteString(fmt.Sprintf("Posted **%d** comments", len(selected)))
	if len(comments) != len(selected) {
		sb.WriteString(fmt.Sprintf(" (of %d found)", len(comments)))
	}
	sb.WriteString(":\n\n")

	if errors > 0 {
		sb.WriteString(fmt.Sprintf("- **Errors**: %d\n", errors))
	}
	if warnings > 0 {
		sb.WriteString(fmt.Sprintf("- **Warnings**: %d\n", warnings))
	}
	if nitpicks > 0 {
		sb.WriteString(fmt.Sprintf("- **Nitpicks**: %d\n", nitpicks))
	}
	if other > 0 {
		sb.WriteString(fmt.Sprintf("- **Other**: %d\n", other))
	}

	return sb.String()
}

// truncateStr truncates a string to maxLen, appending "..." if truncated.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
