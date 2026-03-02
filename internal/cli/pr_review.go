package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/alanmeadows/otto/internal/llm"
	"github.com/alanmeadows/otto/internal/prompts"
	"github.com/alanmeadows/otto/internal/provider"
	"github.com/alanmeadows/otto/internal/repo"
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
	Use:   "review <url> [guidance]",
	Short: "Review a pull request",
	Long: `Perform an LLM-powered code review on a pull request.

Otto clones/checks-out the PR branch, sends the diff and codebase
context to the LLM, and presents the resulting review comments in a
table. You then interactively select which comments to post as
inline review comments on the PR. A summary comment is posted
automatically.

An optional guidance argument can be provided to steer the review
focus, e.g. "focus on error handling" or "check for race conditions
in the worker pool".`,
	Example: `  otto pr review https://github.com/org/repo/pull/42
  otto pr review https://dev.azure.com/org/project/_git/repo/pullrequest/123
  otto pr review https://github.com/org/repo/pull/42 "focus on error handling and concurrency safety"`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		prURL := args[0]
		var guidance string
		if len(args) > 1 {
			guidance = args[1]
		}

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

		// Step 3: Map to local repo, or clone to temp dir for untracked repos.
		workDir, cleanup, err := repo.MapPRToWorkDir(appConfig, prInfo.URL, prInfo.SourceBranch)
		if err != nil {
			// Repo not tracked locally — clone to a temp directory for review.
			slog.Info("repo not tracked locally, cloning to temp dir for review")
			workDir, cleanup, err = cloneForReview(prInfo)
			if err != nil {
				return fmt.Errorf("cloning repo for review: %w", err)
			}
		}
		if cleanup != nil {
			defer cleanup()
		}

		// Analyze codebase for review context.
		summary, err := repo.AnalyzeCodebase(workDir)
		if err != nil {
			slog.Warn("codebase analysis failed, continuing without summary", "error", err)
		}

		// Step 4: Create LLM client.
		llmClient := llm.NewCopilotClient(appConfig.Models.Primary)
		if err := llmClient.Start(ctx); err != nil {
			return fmt.Errorf("starting Copilot LLM client: %w", err)
		}
		defer llmClient.Stop()

		// Step 6: Send pr-review.md prompt.
		templateData := map[string]string{
			"pr_title":       prInfo.Title,
			"pr_description": prInfo.Description,
			"target_branch":  prInfo.TargetBranch,
		}
		if summary != nil {
			templateData["codebase_summary"] = summary.String()
		}
		if guidance != "" {
			templateData["guidance"] = guidance
		}

		prompt, err := prompts.Execute("pr-review.md", templateData)
		if err != nil {
			return fmt.Errorf("building review prompt: %w", err)
		}

		session, err := llmClient.CreateSession(ctx, fmt.Sprintf("PR Review #%s", prInfo.ID), workDir)
		if err != nil {
			return fmt.Errorf("creating review session: %w", err)
		}
		defer llmClient.DeleteSession(ctx, session.ID)

		fmt.Fprintf(cmd.OutOrStdout(), "Analyzing changes against %s...\n", prInfo.TargetBranch)

		resp, err := llmClient.SendPrompt(ctx, session.ID, prompt)
		if err != nil {
			return fmt.Errorf("review prompt failed: %w", err)
		}

		slog.Info("review response", "content_length", len(resp.Content))
		if len(resp.Content) == 0 {
			slog.Warn("LLM returned empty response for PR review")
		}

		// Step 7: Parse JSON response.
		comments, err := llm.ParseJSONResponse[[]reviewComment](ctx, llmClient, session.ID, resp.Content)
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

// truncateStr truncates a string to maxLen, appending "..." if truncated.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// cloneForReview clones a PR's repo to a temp directory and checks out the source branch.
func cloneForReview(prInfo *provider.PRInfo) (string, func(), error) {
// Derive clone URL from PR URL.
cloneURL := deriveCloneURL(prInfo.URL)
if cloneURL == "" {
return "", nil, fmt.Errorf("cannot derive clone URL from PR URL: %s", prInfo.URL)
}

// Create temp directory.
tmpDir, err := os.MkdirTemp("", "otto-review-*")
if err != nil {
return "", nil, fmt.Errorf("creating temp dir: %w", err)
}
cleanup := func() { os.RemoveAll(tmpDir) }

// Clone.
slog.Info("cloning repo for review", "url", cloneURL, "branch", prInfo.SourceBranch)
cmd := exec.Command("git", "clone", "--depth", "50", "--branch", prInfo.SourceBranch, cloneURL, tmpDir)
cmd.Stderr = os.Stderr
if err := cmd.Run(); err != nil {
cleanup()
return "", nil, fmt.Errorf("git clone failed: %w", err)
}

// Fetch the target branch for diff.
fetchCmd := exec.Command("git", "fetch", "origin", prInfo.TargetBranch)
fetchCmd.Dir = tmpDir
fetchCmd.Run() // best effort

return tmpDir, cleanup, nil
}

// deriveCloneURL extracts a git clone URL from a PR web URL.
func deriveCloneURL(prURL string) string {
// GitHub: https://github.com/owner/repo/pull/123 -> https://github.com/owner/repo.git
if strings.Contains(prURL, "github.com") {
parts := strings.Split(prURL, "/")
for i, p := range parts {
if p == "pull" && i >= 2 {
return strings.Join(parts[:i], "/") + ".git"
}
}
}
// ADO: https://dev.azure.com/org/project/_git/repo/pullrequest/123
if strings.Contains(prURL, "dev.azure.com") || strings.Contains(prURL, "visualstudio.com") {
parts := strings.Split(prURL, "/")
for i, p := range parts {
if p == "pullrequest" && i >= 1 {
return strings.Join(parts[:i], "/")
}
}
}
return ""
}
