package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/opencode"
	"github.com/alanmeadows/otto/internal/prompts"
	"github.com/alanmeadows/otto/internal/provider"
	"github.com/alanmeadows/otto/internal/provider/ado"
	ghbackend "github.com/alanmeadows/otto/internal/provider/github"
	"github.com/alanmeadows/otto/internal/repo"
	"github.com/alanmeadows/otto/internal/store"
)

// PRDocument represents a tracked pull request with its lifecycle state.
type PRDocument struct {
	ID             string   `yaml:"id"`
	Title          string   `yaml:"title"`
	Provider       string   `yaml:"provider"`
	Repo           string   `yaml:"repo"`
	Branch         string   `yaml:"branch"`
	Target         string   `yaml:"target"`
	Status         string   `yaml:"status"` // watching, fixing, green, failed, merged, abandoned
	URL            string   `yaml:"url"`
	Created        string   `yaml:"created"`
	LastChecked    string   `yaml:"last_checked"`
	FixAttempts    int      `yaml:"fix_attempts"`
	MaxFixAttempts int      `yaml:"max_fix_attempts"`
	SeenCommentIDs []string `yaml:"seen_comment_ids"`
	Body           string   `yaml:"-"` // markdown body (fix history, etc.)

	// Stage tracking fields — give visibility into what the PR is waiting on.
	MerlinBotDone bool   `yaml:"merlinbot_done"` // true once MerlinBot comments are addressed
	FeedbackDone  bool   `yaml:"feedback_done"`  // true once all review comments are resolved
	PipelineState string `yaml:"pipeline_state"` // pending, running, succeeded, failed, unknown
	HasConflicts  bool   `yaml:"has_conflicts"`  // true when ADO reports merge conflicts
	WaitingOn     string `yaml:"waiting_on"`     // human-readable: "merlinbot", "pipelines", "feedback", "all clear"
}

// ComputeWaitingOn derives the WaitingOn string from the stage tracking fields.
func (pr *PRDocument) ComputeWaitingOn() string {
	if pr.Status == "merged" {
		return "merged"
	}
	if pr.Status == "abandoned" {
		return "abandoned"
	}
	if pr.Status == "failed" {
		return "manual intervention"
	}
	if pr.Status == "fixing" {
		return "fix in progress"
	}

	var waiting []string
	if !pr.MerlinBotDone {
		waiting = append(waiting, "merlinbot")
	}
	if !pr.FeedbackDone {
		waiting = append(waiting, "feedback")
	}
	switch pr.PipelineState {
	case "failed":
		waiting = append(waiting, "pipelines (failed)")
	case "pending", "running", "inProgress", "unknown", "":
		waiting = append(waiting, "pipelines")
		// "succeeded" adds nothing
	}
	if pr.HasConflicts {
		waiting = append(waiting, "merge conflicts")
	}

	if len(waiting) == 0 {
		return "all clear"
	}
	return strings.Join(waiting, ", ")
}

// PRDir returns the global PR storage directory.
func PRDir() string {
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			slog.Error("cannot determine home directory; set $HOME or $XDG_DATA_HOME", "error", err)
			os.Exit(1)
		}
		dataDir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataDir, "otto", "prs")
}

// prFilename generates a filename for a PR document.
func prFilename(providerName, id string) string {
	return fmt.Sprintf("%s__%s.md", providerName, id)
}

// prPath returns the full path for a PR document.
func prPath(providerName, id string) string {
	return filepath.Join(PRDir(), prFilename(providerName, id))
}

// LoadPR loads a PR document from disk.
func LoadPR(providerName, id string) (*PRDocument, error) {
	path := prPath(providerName, id)
	doc, err := store.ReadDocument(path)
	if err != nil {
		return nil, fmt.Errorf("reading PR document: %w", err)
	}

	pr := &PRDocument{}
	pr.Body = doc.Body

	// Map frontmatter fields
	pr.ID = store.GetString(doc.Frontmatter, "id")
	pr.Title = store.GetString(doc.Frontmatter, "title")
	pr.Provider = store.GetString(doc.Frontmatter, "provider")
	pr.Repo = store.GetString(doc.Frontmatter, "repo")
	pr.Branch = store.GetString(doc.Frontmatter, "branch")
	pr.Target = store.GetString(doc.Frontmatter, "target")
	pr.Status = store.GetString(doc.Frontmatter, "status")
	pr.URL = store.GetString(doc.Frontmatter, "url")
	pr.Created = store.GetString(doc.Frontmatter, "created")
	pr.LastChecked = store.GetString(doc.Frontmatter, "last_checked")
	pr.FixAttempts = store.GetInt(doc.Frontmatter, "fix_attempts")
	pr.MaxFixAttempts = store.GetInt(doc.Frontmatter, "max_fix_attempts")
	pr.SeenCommentIDs = store.GetStringSlice(doc.Frontmatter, "seen_comment_ids")
	pr.MerlinBotDone = store.GetBool(doc.Frontmatter, "merlinbot_done")
	pr.FeedbackDone = store.GetBool(doc.Frontmatter, "feedback_done")
	pr.PipelineState = store.GetString(doc.Frontmatter, "pipeline_state")
	pr.WaitingOn = store.GetString(doc.Frontmatter, "waiting_on")

	return pr, nil
}

// SavePR saves a PR document to disk.
func SavePR(pr *PRDocument) error {
	pr.WaitingOn = pr.ComputeWaitingOn()

	fm := map[string]any{
		"id":               pr.ID,
		"title":            pr.Title,
		"provider":         pr.Provider,
		"repo":             pr.Repo,
		"branch":           pr.Branch,
		"target":           pr.Target,
		"status":           pr.Status,
		"url":              pr.URL,
		"created":          pr.Created,
		"last_checked":     pr.LastChecked,
		"fix_attempts":     pr.FixAttempts,
		"max_fix_attempts": pr.MaxFixAttempts,
		"seen_comment_ids": pr.SeenCommentIDs,
		"merlinbot_done":   pr.MerlinBotDone,
		"feedback_done":    pr.FeedbackDone,
		"pipeline_state":   pr.PipelineState,
		"waiting_on":       pr.WaitingOn,
	}

	doc := &store.Document{
		Frontmatter: fm,
		Body:        pr.Body,
	}

	path := prPath(pr.Provider, pr.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating PR directory: %w", err)
	}
	return store.WithLock(path, 5*time.Second, func() error {
		return store.WriteDocument(path, doc)
	})
}

// ListPRs returns all PR documents from the global PR directory.
func ListPRs() ([]*PRDocument, error) {
	dir := PRDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading PR directory: %w", err)
	}

	var prs []*PRDocument
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		// Parse provider and ID from filename: {provider}__{id}.md
		name := strings.TrimSuffix(entry.Name(), ".md")
		parts := strings.SplitN(name, "__", 2)
		if len(parts) != 2 {
			continue
		}
		pr, err := LoadPR(parts[0], parts[1])
		if err != nil {
			slog.Warn("failed to load PR document", "file", entry.Name(), "error", err)
			continue
		}
		prs = append(prs, pr)
	}
	return prs, nil
}

// DeletePR removes a PR document from disk.
func DeletePR(providerName, id string) error {
	path := prPath(providerName, id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing PR document: %w", err)
	}
	return nil
}

// FindPR finds a single PR by ID across all providers.
// If multiple PRs match, returns an error.
func FindPR(id string) (*PRDocument, error) {
	prs, err := ListPRs()
	if err != nil {
		return nil, err
	}

	var matches []*PRDocument
	for _, pr := range prs {
		if pr.ID == id {
			matches = append(matches, pr)
		}
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("PR %s not found", id)
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("ambiguous PR ID %s — found in multiple providers; specify provider", id)
	}
}

// InferPR returns the single tracked PR if only one exists, or errors with guidance.
func InferPR() (*PRDocument, error) {
	prs, err := ListPRs()
	if err != nil {
		return nil, err
	}
	switch len(prs) {
	case 0:
		return nil, fmt.Errorf("no tracked PRs — add one with: otto pr add <url>")
	case 1:
		return prs[0], nil
	default:
		return nil, fmt.Errorf("multiple PRs tracked — specify an ID")
	}
}

// FixPR attempts to fix a failing PR using a two-phase LLM approach.
// Phase 1: Analyze build logs to produce a structured diagnosis.
// Phase 2: Apply fixes based on the diagnosis.
func FixPR(ctx context.Context, pr *PRDocument, backend provider.PRBackend, serverMgr *opencode.ServerManager, cfg *config.Config) (retErr error) {
	// Guard the entire fix operation with a 15-minute deadline so a stuck LLM
	// session cannot block the monitoring loop indefinitely.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	slog.Info("starting PR fix", "prID", pr.ID, "attempt", pr.FixAttempts+1)

	// Set status to "fixing" to prevent concurrent fix attempts.
	pr.Status = "fixing"
	if err := SavePR(pr); err != nil {
		return fmt.Errorf("setting fix status: %w", err)
	}

	// On error, roll status back to "watching" so the monitoring loop retries.
	defer func() {
		if retErr != nil && pr.Status == "fixing" {
			pr.Status = "watching"
			if saveErr := SavePR(pr); saveErr != nil {
				slog.Error("failed to rollback PR status from fixing", "prID", pr.ID, "error", saveErr)
			}
		}
	}()

	// Map PR to local worktree.
	workDir, cleanup, err := repo.MapPRToWorkDir(cfg, pr.URL, pr.Branch)
	if err != nil {
		return fmt.Errorf("mapping PR to workdir: %w", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	llm := serverMgr.LLM()
	if llm == nil {
		return fmt.Errorf("OpenCode server not available")
	}

	// Get pipeline status to find failed builds.
	prInfo := &provider.PRInfo{
		ID:           pr.ID,
		URL:          pr.URL,
		RepoID:       pr.Repo,
		SourceBranch: pr.Branch,
		TargetBranch: pr.Target,
	}

	status, err := backend.GetPipelineStatus(ctx, prInfo)
	if err != nil {
		return fmt.Errorf("getting pipeline status: %w", err)
	}

	// Collect build logs from failed builds.
	var logSummary strings.Builder
	var failedBuildIDs []string
	for _, build := range status.Builds {
		slog.Info("build result", "prID", pr.ID, "buildName", build.Name, "buildID", build.ID, "result", build.Result)
		switch build.Result {
		case "failed", "failure", "partiallySucceeded", "canceled":
			failedBuildIDs = append(failedBuildIDs, build.ID)
		default:
			continue
		}
		logs, err := backend.GetBuildLogs(ctx, prInfo, build.ID)
		if err != nil {
			slog.Warn("failed to get build logs", "buildID", build.ID, "error", err)
			continue
		}
		logSummary.WriteString(fmt.Sprintf("=== Build: %s ===\n%s\n\n", build.Name, logs))
	}

	if logSummary.Len() == 0 {
		return fmt.Errorf("no failed build logs found to analyze")
	}

	model := opencode.ParseModelRef(cfg.Models.Primary)

	// Phase 1: Analyze logs.
	slog.Info("PR fix Phase 1: analyzing build logs", "prID", pr.ID)
	analysisSession, err := llm.CreateSession(ctx, fmt.Sprintf("PR Fix Analysis #%s", pr.ID), workDir)
	if err != nil {
		return fmt.Errorf("creating analysis session: %w", err)
	}
	defer llm.DeleteSession(ctx, analysisSession.ID, workDir)

	analysisPrompt := fmt.Sprintf(`You are analyzing CI/CD build failure logs for PR #%s: "%s".

## Classification (CRITICAL — must be the FIRST line of your response)

Determine the root cause category and write EXACTLY one of these on the very first line:

CLASSIFICATION: INFRASTRUCTURE
CLASSIFICATION: CODE

Use INFRASTRUCTURE when the failure is NOT caused by code in this PR, including:
- Agent/pool unavailability, VM allocation failures, container image pull errors
- Network timeouts, DNS resolution failures, service connection errors
- "No agent found", "Job cancelled", resource quota exceeded
- Transient test failures unrelated to PR changes (flaky tests)
- Pipeline YAML parsing/configuration errors in shared templates
- Artifact download failures, NuGet/npm registry errors
- Any failure that would likely succeed on a simple retry

Use CODE when the failure IS caused by code changes in this PR:
- Compilation errors, syntax errors, type errors
- Test failures caused by logic bugs in changed files
- Linting/formatting violations in changed files
- Missing imports, undefined variables, broken references

## Analysis

Then provide a structured failure summary:
1. Which tests/checks failed
2. The exact error messages
3. File and line locations where errors originate
4. Root cause analysis

## Build Logs

%s

## Output

Provide a concise, structured diagnosis that another LLM can use to fix the code (if CODE) or that explains the infra issue (if INFRASTRUCTURE). Focus on actionable information only.`, pr.ID, pr.Title, logSummary.String())

	analysisResp, err := llm.SendPrompt(ctx, analysisSession.ID, analysisPrompt, model, workDir)
	if err != nil {
		return fmt.Errorf("Phase 1 analysis failed: %w", err)
	}

	diagnosis := analysisResp.Content
	slog.Info("PR fix Phase 1 complete", "diagnosisLength", len(diagnosis))

	// Check if the LLM classified this as an infrastructure failure.
	if isInfraFailure(diagnosis) {
		slog.Info("infrastructure failure detected, retrying builds instead of code fix", "prID", pr.ID, "builds", len(failedBuildIDs))

		var retryErrors []string
		for _, bid := range failedBuildIDs {
			if err := backend.RetryBuild(ctx, prInfo, bid); err != nil {
				slog.Warn("failed to retry build", "buildID", bid, "error", err)
				retryErrors = append(retryErrors, fmt.Sprintf("build %s: %v", bid, err))
			}
		}

		// Infrastructure retries do NOT count against fix attempts.
		pr.Status = "watching"
		pr.PipelineState = "inProgress"
		pr.LastChecked = time.Now().UTC().Format(time.RFC3339)
		pr.Body += fmt.Sprintf("\n\n### Infra Retry - %s\n- **Trigger**: Infrastructure failure detected\n- **Action**: Retried %d build(s)\n- **Diagnosis**: %s\n",
			pr.LastChecked, len(failedBuildIDs), strings.Split(diagnosis, "\n")[0])

		if err := SavePR(pr); err != nil {
			return fmt.Errorf("saving PR after infra retry: %w", err)
		}

		if len(retryErrors) > 0 {
			return fmt.Errorf("some build retries failed: %s", strings.Join(retryErrors, "; "))
		}
		return nil
	}

	// Phase 2: Fix code.
	slog.Info("PR fix Phase 2: applying fixes", "prID", pr.ID)
	fixSession, err := llm.CreateSession(ctx, fmt.Sprintf("PR Fix #%s attempt %d", pr.ID, pr.FixAttempts+1), workDir)
	if err != nil {
		return fmt.Errorf("creating fix session: %w", err)
	}
	defer llm.DeleteSession(ctx, fixSession.ID, workDir)

	fixPrompt := fmt.Sprintf(`You are fixing CI/CD failures for PR #%s: "%s".

## Failure Diagnosis

%s

## Instructions

1. Read the relevant source files mentioned in the diagnosis
2. Fix the identified issues
3. Do NOT introduce unnecessary changes — fix only what's broken
4. Make sure your fixes are correct and complete`, pr.ID, pr.Title, diagnosis)

	_, err = llm.SendPrompt(ctx, fixSession.ID, fixPrompt, model, workDir)
	if err != nil {
		return fmt.Errorf("Phase 2 fix failed: %w", err)
	}

	// Commit and push.
	commitMsg := fmt.Sprintf("fix CI failures (attempt %d)", pr.FixAttempts+1)
	commitHash, err := gitCommitAndPush(ctx, workDir, pr.Branch, commitMsg)
	if err != nil {
		return fmt.Errorf("committing fix: %w", err)
	}

	slog.Info("PR fix committed and pushed", "prID", pr.ID, "commit", commitHash)

	// Update PR document.
	pr.FixAttempts++
	pr.LastChecked = time.Now().UTC().Format(time.RFC3339)
	pr.Body += fmt.Sprintf("\n\n### Attempt %d - %s\n- **Trigger**: Pipeline failure\n- **Action**: %s\n- **Result**: Pending\n- **Commit**: %s\n",
		pr.FixAttempts, pr.LastChecked, strings.Split(diagnosis, "\n")[0], commitHash)

	if pr.FixAttempts >= pr.MaxFixAttempts {
		pr.Status = "failed"
		_ = backend.PostComment(ctx, prInfo, fmt.Sprintf("Exhausted %d fix attempts for this PR. Manual intervention required.", pr.MaxFixAttempts))
		// Notification ownership: FixPR is the sole owner of EventPRFailed notifications.
		// pollSinglePR must NOT send duplicate failure notifications.
		if err := Notify(ctx, &cfg.Notifications, NotificationPayload{
			Event:       EventPRFailed,
			Title:       pr.Title,
			URL:         pr.URL,
			Status:      "failed",
			FixAttempts: pr.FixAttempts,
			MaxAttempts: pr.MaxFixAttempts,
			Error:       "Exhausted fix attempts",
		}); err != nil {
			slog.Warn("failed to send PR failed notification", "prID", pr.ID, "error", err)
		}
	} else {
		pr.Status = "watching"
	}

	return SavePR(pr)
}

// ResolveConflicts attempts to rebase the PR's source branch onto the target
// branch to resolve merge conflicts. If the rebase encounters conflicts that
// git cannot auto-resolve, it uses the LLM to manually resolve them.
func ResolveConflicts(ctx context.Context, pr *PRDocument, backend provider.PRBackend, serverMgr *opencode.ServerManager, cfg *config.Config) error {
	// Guard with a 10-minute deadline so a stuck LLM session cannot block indefinitely.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	slog.Info("starting conflict resolution", "prID", pr.ID, "source", pr.Branch, "target", pr.Target)

	workDir, cleanup, err := repo.MapPRToWorkDir(cfg, pr.URL, pr.Branch)
	if err != nil {
		return fmt.Errorf("mapping PR to workdir: %w", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	// Fetch latest from origin so we have up-to-date refs.
	fetchCmd := exec.CommandContext(ctx, "git", "fetch", "origin")
	fetchCmd.Dir = workDir
	if out, err := fetchCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch: %s: %w", string(out), err)
	}

	// Determine the target branch ref (strip refs/heads/ if present).
	targetRef := pr.Target
	targetRef = strings.TrimPrefix(targetRef, "refs/heads/")

	// Capture what this branch changed relative to the target, so the LLM
	// understands the intent of the branch when resolving conflicts.
	branchSummaryCmd := exec.CommandContext(ctx, "git", "log", "--oneline",
		"origin/"+targetRef+"..HEAD")
	branchSummaryCmd.Dir = workDir
	branchSummaryOut, _ := branchSummaryCmd.Output()

	branchDiffCmd := exec.CommandContext(ctx, "git", "diff", "--stat",
		"origin/"+targetRef+"...HEAD")
	branchDiffCmd.Dir = workDir
	branchDiffOut, _ := branchDiffCmd.Output()

	branchContext := strings.TrimSpace(string(branchSummaryOut))
	branchDiffStat := strings.TrimSpace(string(branchDiffOut))

	// Attempt a rebase onto the target branch.
	rebaseCmd := exec.CommandContext(ctx, "git", "rebase", "origin/"+targetRef)
	rebaseCmd.Dir = workDir
	rebaseOut, rebaseErr := rebaseCmd.CombinedOutput()

	if rebaseErr == nil {
		// Clean rebase — just push.
		slog.Info("rebase succeeded cleanly, pushing", "prID", pr.ID)
		pushCmd := exec.CommandContext(ctx, "git", "push", "--force-with-lease", "origin", "HEAD:"+pr.Branch)
		pushCmd.Dir = workDir
		if out, err := pushCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git push after rebase: %s: %w", string(out), err)
		}

		pr.HasConflicts = false
		slog.Info("merge conflicts resolved via rebase", "prID", pr.ID)
		return SavePR(pr)
	}

	// Rebase failed — there are conflicts git couldn't auto-resolve.
	slog.Warn("rebase has conflicts, using LLM to resolve", "prID", pr.ID, "output", string(rebaseOut))

	llm := serverMgr.LLM()
	if llm == nil {
		// Abort the rebase so the worktree is left clean.
		abortCmd := exec.CommandContext(ctx, "git", "rebase", "--abort")
		abortCmd.Dir = workDir
		_ = abortCmd.Run()
		return fmt.Errorf("OpenCode server not available for conflict resolution")
	}

	// Get the list of conflicted files.
	diffCmd := exec.CommandContext(ctx, "git", "diff", "--name-only", "--diff-filter=U")
	diffCmd.Dir = workDir
	diffOut, err := diffCmd.Output()
	if err != nil {
		abortCmd := exec.CommandContext(ctx, "git", "rebase", "--abort")
		abortCmd.Dir = workDir
		_ = abortCmd.Run()
		return fmt.Errorf("listing conflicted files: %w", err)
	}

	conflictedFiles := strings.TrimSpace(string(diffOut))
	if conflictedFiles == "" {
		// No conflicted files but rebase failed — abort and bail.
		abortCmd := exec.CommandContext(ctx, "git", "rebase", "--abort")
		abortCmd.Dir = workDir
		_ = abortCmd.Run()
		return fmt.Errorf("rebase failed but no conflicted files found")
	}

	model := opencode.ParseModelRef(cfg.Models.Primary)

	resolveSession, err := llm.CreateSession(ctx, fmt.Sprintf("Conflict Resolution #%s", pr.ID), workDir)
	if err != nil {
		abortCmd := exec.CommandContext(ctx, "git", "rebase", "--abort")
		abortCmd.Dir = workDir
		_ = abortCmd.Run()
		return fmt.Errorf("creating conflict resolution session: %w", err)
	}
	defer llm.DeleteSession(ctx, resolveSession.ID, workDir)

	resolvePrompt := fmt.Sprintf(`You are resolving git merge conflicts for PR #%s: "%s".

The source branch (%s) is being rebased onto the target branch (origin/%s).

## Branch Commits

These are the commits on this branch — they represent the intent and purpose of the changes:

%s

## Branch Change Summary

%s

## Conflicted Files

%s

## Instructions

1. First, understand what this branch is doing from the commits and diff stats above
2. Read each conflicted file listed above
3. The files contain git conflict markers (<<<<<<< HEAD, =======, >>>>>>> ...)
4. Resolve each conflict by preserving the intent of this branch's changes while incorporating necessary updates from the target branch
5. When the branch intentionally modified something and the target also changed it, prefer the branch's approach unless it would break the target's changes
6. Remove ALL conflict markers — the files must be valid source code after resolution
7. After resolving all conflicts, stage the files with git add for each resolved file
8. Then run: git rebase --continue

Do NOT introduce unnecessary changes beyond resolving the conflicts.`, pr.ID, pr.Title, pr.Branch, targetRef, branchContext, branchDiffStat, conflictedFiles)

	_, err = llm.SendPrompt(ctx, resolveSession.ID, resolvePrompt, model, workDir)
	if err != nil {
		abortCmd := exec.CommandContext(ctx, "git", "rebase", "--abort")
		abortCmd.Dir = workDir
		_ = abortCmd.Run()
		return fmt.Errorf("LLM conflict resolution failed: %w", err)
	}

	// Verify the rebase completed (no more REBASE_HEAD).
	rebaseHeadCmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "REBASE_HEAD")
	rebaseHeadCmd.Dir = workDir
	if rebaseHeadCmd.Run() == nil {
		// Rebase is still in progress — LLM didn't finish. Abort.
		abortCmd := exec.CommandContext(ctx, "git", "rebase", "--abort")
		abortCmd.Dir = workDir
		_ = abortCmd.Run()
		return fmt.Errorf("LLM did not complete rebase — conflicts may be too complex")
	}

	// Push the rebased branch.
	pushCmd := exec.CommandContext(ctx, "git", "push", "--force-with-lease", "origin", "HEAD:"+pr.Branch)
	pushCmd.Dir = workDir
	if out, err := pushCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push after conflict resolution: %s: %w", string(out), err)
	}

	pr.HasConflicts = false
	slog.Info("merge conflicts resolved via LLM-assisted rebase", "prID", pr.ID)
	return SavePR(pr)
}

// isInfraFailure checks whether the Phase 1 diagnosis classifies the failure
// as an infrastructure issue (not a code bug). It looks for the classification
// marker on the first line of the LLM response.
func isInfraFailure(diagnosis string) bool {
	// Check the first few lines for the classification marker.
	for _, line := range strings.SplitN(diagnosis, "\n", 5) {
		line = strings.TrimSpace(line)
		if strings.EqualFold(line, "CLASSIFICATION: INFRASTRUCTURE") {
			return true
		}
		if strings.HasPrefix(strings.ToUpper(line), "CLASSIFICATION:") {
			// Found a classification line but it's not INFRASTRUCTURE.
			return false
		}
	}
	return false
}

// gitCommitAndPush stages all changes, commits, and pushes to the given branch.
func gitCommitAndPush(ctx context.Context, workDir, branch, message string) (string, error) {
	// Check for changes.
	statusCmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	statusCmd.Dir = workDir
	statusOut, err := statusCmd.Output()
	if err != nil {
		return "", fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(string(statusOut)) == "" {
		return "", fmt.Errorf("no changes to commit")
	}

	// Stage all.
	addCmd := exec.CommandContext(ctx, "git", "add", "-A")
	addCmd.Dir = workDir
	if out, err := addCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git add: %s: %w", string(out), err)
	}

	// Commit.
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", message)
	commitCmd.Dir = workDir
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git commit: %s: %w", string(out), err)
	}

	// Get commit hash.
	hashCmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	hashCmd.Dir = workDir
	hashOut, err := hashCmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	hash := strings.TrimSpace(string(hashOut))

	// Push with explicit refspec so it works regardless of HEAD state
	// (on-branch or detached). Strip refs/heads/ for the local side but
	// keep it for the remote destination.
	shortBranch := strings.TrimPrefix(branch, "refs/heads/")
	refspec := fmt.Sprintf("HEAD:refs/heads/%s", shortBranch)
	pushCmd := exec.CommandContext(ctx, "git", "push", "origin", refspec)
	pushCmd.Dir = workDir
	if out, err := pushCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git push: %s: %w", string(out), err)
	}

	return hash[:8], nil
}

// RunMonitorLoop runs the PR monitoring loop that polls for PR status changes.
// It blocks until the context is cancelled.
func RunMonitorLoop(ctx context.Context, cfg *config.Config, serverMgr *opencode.ServerManager) error {
	pollInterval := cfg.Server.ParsePollInterval()
	slog.Info("starting PR monitoring loop", "interval", pollInterval)

	// Build provider registry from config.
	reg := buildMonitorRegistry(cfg)

	// Reset any PRs stuck in "fixing" from a previous crash/restart.
	resetStuckPRs()

	// Run immediately on startup, then on ticker.
	pollAllPRs(ctx, reg, serverMgr, cfg)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("monitoring loop stopped")
			return nil
		case <-ticker.C:
			pollAllPRs(ctx, reg, serverMgr, cfg)
		case <-pollTrigger:
			slog.Info("immediate poll triggered")
			pollAllPRs(ctx, reg, serverMgr, cfg)
			// Reset ticker so we don't poll again too soon.
			ticker.Reset(pollInterval)
		}
	}
}

// resetStuckPRs resets any PRs left in "fixing" status from a previous
// server crash or restart back to "watching" so the monitor loop picks them up.
func resetStuckPRs() {
	prs, err := ListPRs()
	if err != nil {
		slog.Warn("failed to list PRs for stuck-state reset", "error", err)
		return
	}
	for _, pr := range prs {
		if pr.Status == "fixing" {
			slog.Info("resetting stuck PR from fixing to watching", "prID", pr.ID, "title", pr.Title)
			pr.Status = "watching"
			if err := SavePR(pr); err != nil {
				slog.Error("failed to reset stuck PR", "prID", pr.ID, "error", err)
			}
		}
	}
}

// reapTerminalPRs removes PRs in terminal states (merged, abandoned) whose
// last_checked timestamp is more than 24 hours ago. This keeps the PR list
// tidy without immediately losing visibility after a merge/abandon.
func reapTerminalPRs(prs []*PRDocument) {
	const reapAge = 24 * time.Hour
	now := time.Now().UTC()

	for _, pr := range prs {
		switch pr.Status {
		case "merged", "abandoned":
			// Use LastChecked as the terminal-state timestamp (set when
			// status transitions to merged/abandoned in pollSinglePR).
			if pr.LastChecked == "" {
				continue
			}
			t, err := time.Parse(time.RFC3339, pr.LastChecked)
			if err != nil {
				slog.Warn("cannot parse last_checked for reaping", "prID", pr.ID, "value", pr.LastChecked)
				continue
			}
			if now.Sub(t) >= reapAge {
				slog.Info("reaping terminal PR", "prID", pr.ID, "title", pr.Title, "status", pr.Status, "age", now.Sub(t).Round(time.Minute))
				if err := DeletePR(pr.Provider, pr.ID); err != nil {
					slog.Error("failed to reap PR", "prID", pr.ID, "error", err)
				}
			}
		}
	}
}

// buildMonitorRegistry creates a provider registry from config (server-side).
func buildMonitorRegistry(cfg *config.Config) *provider.Registry {
	reg := provider.NewRegistry()
	if cfg.PR.Providers != nil {
		if adoCfg, ok := cfg.PR.Providers["ado"]; ok {
			auth := ado.NewAuthProvider(adoCfg.PAT)
			adoBackend := ado.NewBackend(adoCfg.Organization, adoCfg.Project, auth)
			reg.Register(adoBackend)
		}
		if ghCfg, ok := cfg.PR.Providers["github"]; ok {
			ghBack := ghbackend.NewBackend("", "", ghCfg.Token)
			reg.Register(ghBack)
		}
	}
	return reg
}

// pollAllPRs processes all tracked PRs in a single poll cycle.
func pollAllPRs(ctx context.Context, reg *provider.Registry, serverMgr *opencode.ServerManager, cfg *config.Config) {
	prs, err := ListPRs()
	if err != nil {
		slog.Error("failed to list PRs", "error", err)
		return
	}

	// Reap terminal PRs (merged/abandoned) older than 24 hours.
	reapTerminalPRs(prs)

	watchCount := 0
	for _, pr := range prs {
		// Bail early if the server is shutting down.
		if ctx.Err() != nil {
			break
		}
		// Only skip terminal states and in-progress fixes.
		switch pr.Status {
		case "merged", "abandoned", "fixing":
			continue
		}
		watchCount++

		slog.Info("polling PR", "prID", pr.ID, "title", pr.Title, "status", pr.Status, "waitingOn", pr.ComputeWaitingOn())
		if err := pollSinglePR(ctx, pr, reg, serverMgr, cfg); err != nil {
			// If auth is broken, skip remaining PRs — they'll all fail the same way.
			if errors.Is(err, ado.ErrAuthExpired) {
				slog.Error("authentication expired, skipping remaining PRs — run 'az login' to refresh", "prID", pr.ID)
				break
			}
			slog.Error("failed to poll PR", "prID", pr.ID, "error", err)
		}
	}

	if watchCount == 0 {
		slog.Debug("no active PRs to poll", "total", len(prs))
	} else {
		slog.Info("poll cycle complete", "polled", watchCount, "total", len(prs))
	}
}

// pollSinglePR handles a single PR check in the monitoring loop.
func pollSinglePR(ctx context.Context, pr *PRDocument, reg *provider.Registry, serverMgr *opencode.ServerManager, cfg *config.Config) error {
	// Short-circuit if context is already cancelled (server shutting down).
	if ctx.Err() != nil {
		return ctx.Err()
	}

	backend, err := reg.Get(pr.Provider)
	if err != nil {
		return fmt.Errorf("getting backend for %s: %w", pr.Provider, err)
	}

	prInfo := &provider.PRInfo{
		ID:           pr.ID,
		URL:          pr.URL,
		RepoID:       pr.Repo,
		SourceBranch: pr.Branch,
		TargetBranch: pr.Target,
	}

	// 0. Check if PR has been merged or abandoned.
	latestPR, err := backend.GetPR(ctx, pr.URL)
	if err != nil {
		if errors.Is(err, ado.ErrAuthExpired) {
			return err
		}
		slog.Warn("failed to check PR status", "prID", pr.ID, "error", err)
	} else {
		// Keep document in sync with live PR metadata.
		if latestPR.Title != "" && latestPR.Title != pr.Title {
			slog.Info("PR title updated", "prID", pr.ID, "old", pr.Title, "new", latestPR.Title)
			pr.Title = latestPR.Title
		}

		switch latestPR.Status {
		case "completed":
			slog.Info("PR has been merged", "prID", pr.ID)
			pr.Status = "merged"
			pr.LastChecked = time.Now().UTC().Format(time.RFC3339)
			return SavePR(pr)
		case "abandoned":
			slog.Info("PR has been abandoned", "prID", pr.ID)
			pr.Status = "abandoned"
			pr.LastChecked = time.Now().UTC().Format(time.RFC3339)
			return SavePR(pr)
		}

		// Check for merge conflicts.
		if latestPR.MergeStatus == "conflicts" {
			if !pr.HasConflicts {
				slog.Warn("PR has merge conflicts, attempting rebase", "prID", pr.ID)
				pr.HasConflicts = true
				if err := SavePR(pr); err != nil {
					slog.Error("failed to save conflict state", "prID", pr.ID, "error", err)
				}
				if err := ResolveConflicts(ctx, pr, backend, serverMgr, cfg); err != nil {
					slog.Error("failed to resolve merge conflicts", "prID", pr.ID, "error", err)
				}
			} else {
				slog.Info("PR still has merge conflicts (already attempted)", "prID", pr.ID)
			}
		} else {
			if pr.HasConflicts {
				slog.Info("merge conflicts resolved", "prID", pr.ID)
				pr.HasConflicts = false
			}
		}
	}

	// 1. Check pipeline status.
	status, err := backend.GetPipelineStatus(ctx, prInfo)
	if err != nil {
		if errors.Is(err, ado.ErrAuthExpired) {
			return err
		}
		slog.Warn("failed to get pipeline status", "prID", pr.ID, "error", err)
		pr.PipelineState = "unknown"
	} else {
		pr.PipelineState = status.State
		slog.Info("pipeline status", "prID", pr.ID, "state", status.State)

		switch status.State {
		case "succeeded":
			// Notify once when transitioning to green.
			if pr.Status != "green" {
				pr.Status = "green"
				if err := Notify(ctx, &cfg.Notifications, NotificationPayload{
					Event:  EventPRGreen,
					Title:  pr.Title,
					URL:    pr.URL,
					Status: "green",
				}); err != nil {
					slog.Warn("failed to send PR green notification", "prID", pr.ID, "error", err)
				}
			}
			// Fall through to check comments and MerlinBot.

		case "failed":
			pr.Status = "watching"
			slog.Info("pipeline failed, attempting fix", "prID", pr.ID)
			if pr.FixAttempts < pr.MaxFixAttempts {
				if fixErr := FixPR(ctx, pr, backend, serverMgr, cfg); fixErr != nil {
					slog.Error("fix attempt failed", "prID", pr.ID, "error", fixErr)
				}
			} else {
				pr.Status = "failed"
				slog.Warn("max fix attempts reached", "prID", pr.ID)
			}

		default:
			// inProgress, pending, unknown — pipeline not yet decided.
			if pr.Status == "green" {
				// Pipeline was green but now running again (new push).
				pr.Status = "watching"
			}
		}
	}

	// 2. Check for new comments.
	newCommentCount := 0
	unresolvedCount := 0
	comments, err := backend.GetComments(ctx, prInfo)
	if err != nil {
		if errors.Is(err, ado.ErrAuthExpired) {
			return err
		}
		slog.Warn("failed to get comments", "prID", pr.ID, "error", err)
	} else {
		seenSet := make(map[string]bool)
		for _, id := range pr.SeenCommentIDs {
			seenSet[id] = true
		}

		for _, comment := range comments {
			// Skip MerlinBot-authored comments — tracked via MerlinBotDone in section 3.
			if isMerlinBotAuthor(comment.Author) {
				continue
			}
			// Skip system-generated comments (auto-complete, reviewer updates, ref changes, etc.).
			if comment.CommentType == "system" {
				continue
			}
			if !comment.IsResolved {
				unresolvedCount++
			}
			// Use composite key (threadID:commentID) since comment IDs are only unique within a thread.
			commentKey := fmt.Sprintf("%s:%s", comment.ThreadID, comment.ID)
			if seenSet[commentKey] || comment.IsResolved {
				continue
			}

			slog.Info("processing new comment", "prID", pr.ID, "commentID", comment.ID, "author", comment.Author)
			if err := evaluateComment(ctx, pr, comment, backend, serverMgr, cfg); err != nil {
				slog.Error("failed to evaluate comment", "prID", pr.ID, "commentID", comment.ID, "error", err)
			}
			newCommentCount++
		}

		// Track feedback resolution state.
		pr.FeedbackDone = unresolvedCount == 0
		slog.Info("comment status", "prID", pr.ID, "new", newCommentCount, "unresolved", unresolvedCount, "feedbackDone", pr.FeedbackDone)

		// Reload PR from disk to get updates from evaluateComment.
		reloaded, loadErr := LoadPR(pr.Provider, pr.ID)
		if loadErr != nil {
			return fmt.Errorf("failed to reload PR after comment processing: %w", loadErr)
		}
		// Preserve stage fields we just computed (evaluateComment doesn't update these).
		reloaded.MerlinBotDone = pr.MerlinBotDone
		reloaded.FeedbackDone = pr.FeedbackDone
		reloaded.PipelineState = pr.PipelineState
		pr = reloaded
	}

	// 3. MerlinBot handling — detect presence, evaluate, and resolve.
	if !pr.MerlinBotDone {
		if pr.Provider != "ado" {
			pr.MerlinBotDone = true
		} else if adoCfg, ok := cfg.PR.Providers["ado"]; !ok || !adoCfg.MerlinBot {
			pr.MerlinBotDone = true
			slog.Debug("MerlinBot not configured, marking done", "prID", pr.ID)
		} else if comments != nil {
			if err := handleMerlinBotDaemon(ctx, pr, comments, backend, serverMgr, cfg); err != nil {
				slog.Warn("MerlinBot handling failed", "prID", pr.ID, "error", err)
			}
		}
	}

	// 4. Notify if comments were handled.
	if newCommentCount > 0 {
		if err := Notify(ctx, &cfg.Notifications, NotificationPayload{
			Event: EventCommentHandled,
			Title: pr.Title,
			URL:   pr.URL,
			Extra: map[string]string{"comments_handled": fmt.Sprintf("%d", newCommentCount)},
		}); err != nil {
			slog.Warn("failed to send comment handled notification", "prID", pr.ID, "error", err)
		}
	}

	pr.LastChecked = time.Now().UTC().Format(time.RFC3339)
	return SavePR(pr)
}

// isMerlinBotAuthor returns true if the comment author is MerlinBot.
func isMerlinBotAuthor(author string) bool {
	return strings.Contains(author, "MerlinBot") || strings.Contains(author, "Merlin")
}

// filterAllMerlinBotComments returns all MerlinBot comments regardless of resolution status.
func filterAllMerlinBotComments(comments []provider.Comment) []provider.Comment {
	var bot []provider.Comment
	for _, c := range comments {
		if isMerlinBotAuthor(c.Author) {
			bot = append(bot, c)
		}
	}
	return bot
}

// merlinBotEvaluation represents a parsed evaluation of a single MerlinBot comment.
type merlinBotEvaluation struct {
	ThreadID string
	Decision string // FIX, WONT_FIX, BY_DESIGN
	Reason   string
	Action   string
}

// parseMerlinBotEvaluation parses the LLM's structured evaluation output.
func parseMerlinBotEvaluation(content string) []merlinBotEvaluation {
	var evals []merlinBotEvaluation

	threadRe := regexp.MustCompile(`THREAD\s+(\S+):\s*(FIX|WONT_FIX|BY_DESIGN)`)
	reasonRe := regexp.MustCompile(`REASON:\s*(.+)`)
	actionRe := regexp.MustCompile(`ACTION:\s*(.+)`)

	lines := strings.Split(content, "\n")
	var current *merlinBotEvaluation

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if m := threadRe.FindStringSubmatch(line); m != nil {
			if current != nil {
				evals = append(evals, *current)
			}
			current = &merlinBotEvaluation{
				ThreadID: m[1],
				Decision: m[2],
			}
			continue
		}

		if current != nil {
			if m := reasonRe.FindStringSubmatch(line); m != nil {
				current.Reason = m[1]
			}
			if m := actionRe.FindStringSubmatch(line); m != nil {
				current.Action = m[1]
			}
		}
	}

	if current != nil {
		evals = append(evals, *current)
	}

	return evals
}

// handleMerlinBotDaemon processes MerlinBot comments on an ADO PR.
// It detects "no AI feedback", evaluates real feedback via LLM, and resolves threads.
// Sets pr.MerlinBotDone = true when MerlinBot has been fully handled.
func handleMerlinBotDaemon(ctx context.Context, pr *PRDocument, comments []provider.Comment, backend provider.PRBackend, serverMgr *opencode.ServerManager, cfg *config.Config) error {
	prInfo := &provider.PRInfo{
		ID:           pr.ID,
		URL:          pr.URL,
		RepoID:       pr.Repo,
		SourceBranch: pr.Branch,
		TargetBranch: pr.Target,
	}

	// Find ALL MerlinBot comments (including resolved — MerlinBot auto-closes
	// "no AI feedback" threads immediately).
	allBotComments := filterAllMerlinBotComments(comments)
	if len(allBotComments) == 0 {
		// Log unique authors to help diagnose matching issues.
		authorSet := make(map[string]bool)
		for _, c := range comments {
			authorSet[c.Author] = true
		}
		var authors []string
		for a := range authorSet {
			authors = append(authors, a)
		}
		slog.Info("no MerlinBot comments found yet", "prID", pr.ID, "totalComments", len(comments), "authors", strings.Join(authors, ", "))
		return nil
	}

	slog.Info("found MerlinBot comments", "prID", pr.ID, "count", len(allBotComments))

	// Short-circuit: "no AI feedback" — MerlinBot has nothing to say.
	for _, c := range allBotComments {
		if strings.Contains(c.Body, "There is no AI feedback on this pull request") {
			slog.Info("MerlinBot reports no AI feedback", "prID", pr.ID)
			// Close any still-open "no feedback" threads.
			for _, bc := range allBotComments {
				if !bc.IsResolved && strings.Contains(bc.Body, "There is no AI feedback on this pull request") {
					if err := backend.ResolveComment(ctx, prInfo, bc.ThreadID, provider.ResolutionByDesign); err != nil {
						slog.Warn("failed to close no-feedback thread", "prID", pr.ID, "threadID", bc.ThreadID, "error", err)
					}
				}
			}
			pr.MerlinBotDone = true
			return nil
		}
	}

	// Filter to only unresolved MerlinBot comments.
	var unresolvedBot []provider.Comment
	for _, c := range allBotComments {
		if !c.IsResolved {
			unresolvedBot = append(unresolvedBot, c)
		}
	}

	if len(unresolvedBot) == 0 {
		slog.Info("all MerlinBot comments already resolved", "prID", pr.ID)
		pr.MerlinBotDone = true
		return nil
	}

	// Evaluate unresolved MerlinBot comments via LLM.
	slog.Info("evaluating MerlinBot comments via LLM", "prID", pr.ID, "count", len(unresolvedBot))

	workDir, cleanup, err := repo.MapPRToWorkDir(cfg, pr.URL, pr.Branch)
	if err != nil {
		return fmt.Errorf("mapping PR to workdir: %w", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	llm := serverMgr.LLM()
	if llm == nil {
		return fmt.Errorf("OpenCode server not available")
	}

	model := opencode.ParseModelRef(cfg.Models.Primary)

	// Build comment summary for the prompt.
	var commentSummary strings.Builder
	for _, c := range unresolvedBot {
		commentSummary.WriteString(fmt.Sprintf("THREAD %s [%s:%d]:\n%s\n\n", c.ThreadID, c.FilePath, c.Line, c.Body))
	}

	templateData := map[string]string{
		"Comments": commentSummary.String(),
	}
	prompt, err := prompts.Execute("merlinbot-evaluate.md", templateData)
	if err != nil {
		return fmt.Errorf("building MerlinBot evaluation prompt: %w", err)
	}

	session, err := llm.CreateSession(ctx, fmt.Sprintf("MerlinBot PR#%s", pr.ID), workDir)
	if err != nil {
		return fmt.Errorf("creating MerlinBot session: %w", err)
	}
	defer llm.DeleteSession(ctx, session.ID, workDir)

	resp, err := llm.SendPrompt(ctx, session.ID, prompt, model, workDir)
	if err != nil {
		return fmt.Errorf("MerlinBot evaluation failed: %w", err)
	}

	// Parse evaluations and take action.
	evaluations := parseMerlinBotEvaluation(resp.Content)
	fixCount := 0

	for _, eval := range evaluations {
		switch eval.Decision {
		case "FIX":
			fixCount++
			fixPrompt := fmt.Sprintf("Fix the issue identified by MerlinBot in thread %s:\n\n%s\n\nAction: %s",
				eval.ThreadID, eval.Reason, eval.Action)
			if _, err := llm.SendPrompt(ctx, session.ID, fixPrompt, model, workDir); err != nil {
				slog.Warn("failed to apply MerlinBot fix", "prID", pr.ID, "threadID", eval.ThreadID, "error", err)
				continue
			}
			if err := backend.ReplyToComment(ctx, prInfo, eval.ThreadID, fmt.Sprintf("Fixed: %s", eval.Action)); err != nil {
				slog.Warn("failed to reply to MerlinBot thread", "prID", pr.ID, "threadID", eval.ThreadID, "error", err)
			}
			if err := backend.ResolveComment(ctx, prInfo, eval.ThreadID, provider.ResolutionFixed); err != nil {
				slog.Warn("failed to resolve MerlinBot thread", "prID", pr.ID, "threadID", eval.ThreadID, "error", err)
			}

		case "WONT_FIX":
			if err := backend.ReplyToComment(ctx, prInfo, eval.ThreadID, eval.Reason); err != nil {
				slog.Warn("failed to reply to MerlinBot thread", "prID", pr.ID, "threadID", eval.ThreadID, "error", err)
			}
			if err := backend.ResolveComment(ctx, prInfo, eval.ThreadID, provider.ResolutionWontFix); err != nil {
				slog.Warn("failed to resolve MerlinBot thread", "prID", pr.ID, "threadID", eval.ThreadID, "error", err)
			}

		case "BY_DESIGN":
			if err := backend.ReplyToComment(ctx, prInfo, eval.ThreadID, eval.Reason); err != nil {
				slog.Warn("failed to reply to MerlinBot thread", "prID", pr.ID, "threadID", eval.ThreadID, "error", err)
			}
			if err := backend.ResolveComment(ctx, prInfo, eval.ThreadID, provider.ResolutionByDesign); err != nil {
				slog.Warn("failed to resolve MerlinBot thread", "prID", pr.ID, "threadID", eval.ThreadID, "error", err)
			}
		}
	}

	// Commit and push if fixes were applied.
	if fixCount > 0 {
		commitMsg := fmt.Sprintf("address %d MerlinBot comment(s)", fixCount)
		commitHash, err := gitCommitAndPush(ctx, workDir, pr.Branch, commitMsg)
		if err != nil {
			slog.Warn("failed to commit MerlinBot fixes", "prID", pr.ID, "error", err)
		} else {
			slog.Info("committed MerlinBot fixes", "prID", pr.ID, "commit", commitHash, "fixCount", fixCount)
		}
	}

	pr.MerlinBotDone = true
	slog.Info("MerlinBot handling complete", "prID", pr.ID, "evaluated", len(evaluations), "fixed", fixCount)
	return nil
}
