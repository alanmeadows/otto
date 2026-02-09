package server

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/opencode"
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
	Status         string   `yaml:"status"` // watching, fixing, green, failed, abandoned
	URL            string   `yaml:"url"`
	Created        string   `yaml:"created"`
	LastChecked    string   `yaml:"last_checked"`
	FixAttempts    int      `yaml:"fix_attempts"`
	MaxFixAttempts int      `yaml:"max_fix_attempts"`
	SeenCommentIDs []string `yaml:"seen_comment_ids"`
	Body           string   `yaml:"-"` // markdown body (fix history, etc.)
}

// PRDir returns the global PR storage directory.
func PRDir() string {
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		home, _ := os.UserHomeDir()
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

	return pr, nil
}

// SavePR saves a PR document to disk.
func SavePR(pr *PRDocument) error {
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
func FixPR(ctx context.Context, pr *PRDocument, backend provider.PRBackend, serverMgr *opencode.ServerManager, cfg *config.Config) error {
	slog.Info("starting PR fix", "prID", pr.ID, "attempt", pr.FixAttempts+1)

	// Set status to "fixing" to prevent concurrent fix attempts.
	pr.Status = "fixing"
	if err := SavePR(pr); err != nil {
		return fmt.Errorf("setting fix status: %w", err)
	}

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
	for _, build := range status.Builds {
		if build.Result != "failed" && build.Result != "failure" {
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

Distill these logs into a structured failure summary:
1. Which tests/checks failed
2. The exact error messages
3. File and line locations where errors originate
4. Root cause analysis

## Build Logs

%s

## Output

Provide a concise, structured diagnosis that another LLM can use to fix the code. Focus on actionable information only.`, pr.ID, pr.Title, logSummary.String())

	analysisResp, err := llm.SendPrompt(ctx, analysisSession.ID, analysisPrompt, model, workDir)
	if err != nil {
		return fmt.Errorf("Phase 1 analysis failed: %w", err)
	}

	diagnosis := analysisResp.Content
	slog.Info("PR fix Phase 1 complete", "diagnosisLength", len(diagnosis))

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
	commitMsg := fmt.Sprintf("otto: fix CI failures (attempt %d)", pr.FixAttempts+1)
	commitHash, err := gitCommitAndPush(ctx, workDir, commitMsg)
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
		_ = backend.PostComment(ctx, prInfo, fmt.Sprintf("otto: Exhausted %d fix attempts for this PR. Manual intervention required.", pr.MaxFixAttempts))
	} else {
		pr.Status = "watching"
	}

	return SavePR(pr)
}

// gitCommitAndPush stages all changes, commits, and pushes.
func gitCommitAndPush(ctx context.Context, workDir, message string) (string, error) {
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

	// Push.
	pushCmd := exec.CommandContext(ctx, "git", "push")
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

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("monitoring loop stopped")
			return nil
		case <-ticker.C:
			pollAllPRs(ctx, reg, serverMgr, cfg)
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

	for _, pr := range prs {
		if pr.Status != "watching" {
			continue
		}

		if err := pollSinglePR(ctx, pr, reg, serverMgr, cfg); err != nil {
			slog.Error("failed to poll PR", "prID", pr.ID, "error", err)
		}
	}
}

// pollSinglePR handles a single PR check in the monitoring loop.
func pollSinglePR(ctx context.Context, pr *PRDocument, reg *provider.Registry, serverMgr *opencode.ServerManager, cfg *config.Config) error {
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

	// 1. Check pipeline status.
	status, err := backend.GetPipelineStatus(ctx, prInfo)
	if err != nil {
		slog.Warn("failed to get pipeline status", "prID", pr.ID, "error", err)
	} else {
		switch status.State {
		case "succeeded":
			pr.Status = "green"
			pr.LastChecked = time.Now().UTC().Format(time.RFC3339)
			_ = backend.PostComment(ctx, prInfo, "otto: All pipelines passed! ✅")
			return SavePR(pr)

		case "failed":
			slog.Info("pipeline failed, attempting fix", "prID", pr.ID)
			if pr.FixAttempts < pr.MaxFixAttempts {
				if fixErr := FixPR(ctx, pr, backend, serverMgr, cfg); fixErr != nil {
					slog.Error("fix attempt failed", "prID", pr.ID, "error", fixErr)
				}
			} else {
				slog.Warn("max fix attempts reached", "prID", pr.ID)
			}
		}
	}

	// 2. Check for new comments.
	newCommentCount := 0
	comments, err := backend.GetComments(ctx, prInfo)
	if err != nil {
		slog.Warn("failed to get comments", "prID", pr.ID, "error", err)
	} else {
		seenSet := make(map[string]bool)
		for _, id := range pr.SeenCommentIDs {
			seenSet[id] = true
		}

		for _, comment := range comments {
			if seenSet[comment.ID] || comment.IsResolved {
				continue
			}

			slog.Info("processing new comment", "prID", pr.ID, "commentID", comment.ID, "author", comment.Author)
			if err := evaluateComment(ctx, pr, comment, backend, serverMgr, cfg); err != nil {
				slog.Error("failed to evaluate comment", "prID", pr.ID, "commentID", comment.ID, "error", err)
			}
			newCommentCount++
		}

		// Reload PR from disk to get updates from evaluateComment.
		reloaded, loadErr := LoadPR(pr.Provider, pr.ID)
		if loadErr != nil {
			slog.Warn("failed to reload PR after comments", "error", loadErr)
		} else {
			pr = reloaded
		}
	}

	// 3. ADO MerlinBot handling — only when new comments found.
	if pr.Provider == "ado" && newCommentCount > 0 {
		_ = backend.RunWorkflow(ctx, prInfo, provider.WorkflowAddressBot)
	}

	pr.LastChecked = time.Now().UTC().Format(time.RFC3339)
	return SavePR(pr)
}
