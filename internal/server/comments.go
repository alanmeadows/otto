package server

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/opencode"
	"github.com/alanmeadows/otto/internal/prompts"
	"github.com/alanmeadows/otto/internal/provider"
	"github.com/alanmeadows/otto/internal/repo"
)

// CommentResponse represents the LLM's evaluation of a PR comment.
type CommentResponse struct {
	Decision       string `json:"decision"` // AGREE, BY_DESIGN, WONT_FIX
	Reply          string `json:"reply"`
	FixDescription string `json:"fix_description,omitempty"`
}

// evaluateComment processes a new review comment on a tracked PR.
// It creates an LLM session, evaluates the comment, and takes appropriate action.
func evaluateComment(ctx context.Context, pr *PRDocument, comment provider.Comment, backend provider.PRBackend, serverMgr *opencode.ServerManager, cfg *config.Config) error {
	slog.Info("evaluating comment", "prID", pr.ID, "commentID", comment.ID, "author", comment.Author)

	// Map PR to local worktree.
	workDir, cleanup, err := repo.MapPRToWorkDir(cfg, pr.URL, pr.Branch)
	if err != nil {
		return fmt.Errorf("mapping PR to workdir: %w", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	// Ensure OpenCode permissions.
	if err := opencode.EnsurePermissions(workDir); err != nil {
		return fmt.Errorf("ensuring permissions: %w", err)
	}

	llm := serverMgr.LLM()
	if llm == nil {
		return fmt.Errorf("OpenCode server not available")
	}

	model := opencode.ParseModelRef(cfg.Models.Primary)

	// Read code context around the commented line.
	codeContext := readCodeContext(workDir, comment.FilePath, comment.Line, 10)

	// Build the prompt using the pr-comment-respond template.
	templateData := map[string]string{
		"pr_title":       pr.Title,
		"pr_description": "",
		"comment_author": comment.Author,
		"comment_file":   comment.FilePath,
		"comment_line":   fmt.Sprintf("%d", comment.Line),
		"comment_body":   comment.Body,
		"code_context":   codeContext,
	}

	prompt, err := prompts.Execute("pr-comment-respond.md", templateData)
	if err != nil {
		return fmt.Errorf("building comment response prompt: %w", err)
	}

	// Create session and send prompt.
	session, err := llm.CreateSession(ctx, fmt.Sprintf("Comment Response PR#%s", pr.ID), workDir)
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	defer llm.DeleteSession(ctx, session.ID, workDir)

	resp, err := llm.SendPrompt(ctx, session.ID, prompt, model, workDir)
	if err != nil {
		return fmt.Errorf("sending prompt: %w", err)
	}

	// Parse the response — expect JSON with decision/reply.
	content := resp.Content
	prInfo := &provider.PRInfo{
		ID:           pr.ID,
		URL:          pr.URL,
		RepoID:       pr.Repo,
		SourceBranch: pr.Branch,
		TargetBranch: pr.Target,
	}

	// Parse JSON response using the generic ParseJSONResponse.
	commentResp, err := opencode.ParseJSONResponse[CommentResponse](ctx, llm, session.ID, workDir, model, content)
	if err != nil {
		// Fallback: post the raw response as a reply.
		slog.Warn("failed to parse comment response JSON, posting raw reply", "error", err)
		if err := backend.ReplyToComment(ctx, prInfo, comment.ThreadID, content); err != nil {
			slog.Warn("failed to reply to comment", "error", err, "threadID", comment.ThreadID)
		}
		return nil
	}

	// Reply to the comment.
	if commentResp.Reply != "" {
		if err := backend.ReplyToComment(ctx, prInfo, comment.ThreadID, commentResp.Reply); err != nil {
			slog.Warn("failed to reply to comment", "error", err)
		}
	}

	// Resolve the thread based on decision.
	switch strings.ToUpper(commentResp.Decision) {
	case "AGREE":
		// The LLM should have made code changes in the session.
		// Commit and push if there are changes.
		commitHash, err := gitCommitAndPush(ctx, workDir, fmt.Sprintf("otto: address review comment on %s:%d", comment.FilePath, comment.Line))
		if err != nil {
			slog.Warn("no changes to commit for AGREE decision", "error", err)
		} else {
			// Update the reply with commit hash.
			if err := backend.ReplyToComment(ctx, prInfo, comment.ThreadID, fmt.Sprintf("Fixed in %s", commitHash)); err != nil {
				slog.Warn("failed to reply to comment", "error", err, "threadID", comment.ThreadID)
			}
		}
		if err := backend.ResolveComment(ctx, prInfo, comment.ThreadID, provider.ResolutionFixed); err != nil {
			slog.Warn("failed to resolve comment thread", "error", err, "threadID", comment.ThreadID)
		}

	case "BY_DESIGN":
		if err := backend.ResolveComment(ctx, prInfo, comment.ThreadID, provider.ResolutionByDesign); err != nil {
			slog.Warn("failed to resolve comment thread", "error", err, "threadID", comment.ThreadID)
		}

	case "WONT_FIX":
		if err := backend.ResolveComment(ctx, prInfo, comment.ThreadID, provider.ResolutionWontFix); err != nil {
			slog.Warn("failed to resolve comment thread", "error", err, "threadID", comment.ThreadID)
		}

	default:
		slog.Warn("unknown comment decision", "decision", commentResp.Decision)
	}

	// Update PR document with comment history.
	pr.Body += fmt.Sprintf("\n\n### Comment by %s on %s:%d - %s\n- **Decision**: %s\n- **Reply**: %s\n",
		comment.Author, comment.FilePath, comment.Line,
		time.Now().UTC().Format(time.RFC3339),
		commentResp.Decision, commentResp.Reply)

	// Track the comment as seen.
	pr.SeenCommentIDs = append(pr.SeenCommentIDs, comment.ID)

	// Persist updated PR document.
	if err := SavePR(pr); err != nil {
		slog.Warn("failed to save PR document after comment evaluation", "error", err)
	}

	return nil
}

// readCodeContext reads surrounding lines from a file in the work directory.
func readCodeContext(workDir, filePath string, line, radius int) string {
	if filePath == "" || line <= 0 {
		return ""
	}
	fullPath := filepath.Join(workDir, filePath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	start := line - radius - 1
	if start < 0 {
		start = 0
	}
	end := line + radius
	if end > len(lines) {
		end = len(lines)
	}
	var buf strings.Builder
	for i := start; i < end; i++ {
		buf.WriteString(fmt.Sprintf("%4d | %s\n", i+1, lines[i]))
	}
	return buf.String()
}
