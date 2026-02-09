package spec

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/opencode"
	"github.com/alanmeadows/otto/internal/prompts"
	"github.com/alanmeadows/otto/internal/store"
	"github.com/charmbracelet/lipgloss"
)

// Execute runs the full task execution engine for a spec.
// It processes tasks phase by phase, running tasks within each phase
// concurrently (bounded by MaxParallelTasks), with per-task retry logic,
// phase review gates, question harvesting, and git commits.
func Execute(ctx context.Context, client opencode.LLMClient, cfg *config.Config, repoDir, slug string) error {
	spec, err := ResolveSpec(slug, repoDir)
	if err != nil {
		return err
	}

	if err := CheckPrerequisites(spec, "execute"); err != nil {
		return err
	}

	// Parse tasks.
	tasks, err := ParseTasks(spec.TasksPath)
	if err != nil {
		return fmt.Errorf("parsing tasks: %w", err)
	}

	if len(tasks) == 0 {
		slog.Info("no tasks to execute")
		return nil
	}

	// Crash recovery: reset any "running" tasks to "pending".
	if err := recoverCrashedTasks(spec, tasks); err != nil {
		return fmt.Errorf("crash recovery: %w", err)
	}

	// Re-parse after potential status changes.
	tasks, err = ParseTasks(spec.TasksPath)
	if err != nil {
		return fmt.Errorf("re-parsing tasks after crash recovery: %w", err)
	}

	// Build phases from task parallel groups.
	phases, err := BuildPhases(tasks)
	if err != nil {
		return fmt.Errorf("building phases: %w", err)
	}

	// Ensure OpenCode permissions for automated operation.
	if err := opencode.EnsurePermissions(repoDir); err != nil {
		return fmt.Errorf("ensuring permissions: %w", err)
	}

	maxParallel := cfg.Spec.MaxParallelTasks
	if maxParallel <= 0 {
		maxParallel = 1
	}

	maxRetries := cfg.Spec.MaxTaskRetries
	if maxRetries < 0 {
		maxRetries = 0
	}

	phasesExecuted := 0

	for phaseIdx, phase := range phases {
		phaseNum := phase[0].ParallelGroup

		// Skip completed phases.
		if phaseAllCompleted(phase) {
			slog.Info("skipping completed phase", "phase", phaseNum)
			continue
		}

		slog.Info("starting phase", "phase", phaseNum, "tasks", len(phase))
		printPhaseHeader(phaseNum, phase)

		// Pre-flight overlap check.
		checkFileOverlaps(phase)

		// Record history baseline for question harvesting.
		historyBaseline := nextHistoryNumber(spec.HistoryDir) - 1

		// Run tasks with bounded parallelism and retry.
		results := runPhase(ctx, client, cfg, repoDir, slug, spec, phase, maxParallel, maxRetries)

		// Re-parse tasks to get updated statuses.
		tasks, err = ParseTasks(spec.TasksPath)
		if err != nil {
			slog.Error("failed to re-parse tasks after phase", "phase", phaseNum, "error", err)
		}

		// Print phase results.
		printPhaseResults(phaseNum, results)

		// Check if all tasks in this phase failed — skip commit and review.
		if resultsAllFailed(results) {
			slog.Warn("all tasks in phase failed, skipping commit", "phase", phaseNum)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}

		// Phase review gate (task 4.2).
		reviewPhase(ctx, client, cfg, repoDir, spec, phaseNum)

		// External assumption validation (task 4.6).
		validateExternalAssumptions(ctx, client, cfg, repoDir, spec, phaseNum)

		// Domain hardening (task 4.7).
		hardenDomain(ctx, client, cfg, repoDir, spec, phaseNum)

		// Phase commit (task 4.1e).
		committed := commitPhase(repoDir, phaseNum, phase)
		if committed {
			phasesExecuted++
		}

		// Summary chaining (task 4.5).
		if committed {
			generatePhaseSummary(ctx, client, cfg, repoDir, spec, phaseNum, phase)
		}

		// Question harvesting (task 4.3).
		harvestQuestions(ctx, client, cfg, repoDir, spec, phaseNum, historyBaseline)

		// Progress display (task 4.4).
		printOverallProgress(phaseIdx+1, len(phases), tasks)

		// Check context cancellation between phases.
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	// Documentation alignment (task 4.8) — runs once after all phases complete.
	if phasesExecuted > 0 {
		alignDocumentation(ctx, client, cfg, repoDir, spec)
	}

	fmt.Fprintf(os.Stderr, "\n✓ Execution complete\n")
	return nil
}

// taskResult captures the outcome of a single task execution.
type taskResult struct {
	TaskID  string
	Title   string
	Err     error
	Retries int
}

// recoverCrashedTasks resets any tasks with status "running" to "pending".
// This handles the case where a previous execution crashed mid-task.
func recoverCrashedTasks(spec *Spec, tasks []Task) error {
	for _, t := range tasks {
		if t.Status == TaskStatusRunning {
			slog.Warn("crash recovery: resetting running task to pending", "task", t.ID)
			if err := UpdateTaskStatus(spec.TasksPath, t.ID, TaskStatusPending); err != nil {
				return fmt.Errorf("resetting task %s: %w", t.ID, err)
			}
		}
	}
	return nil
}

// phaseAllCompleted returns true if every task in the phase has status "completed".
func phaseAllCompleted(phase []Task) bool {
	for _, t := range phase {
		if t.Status != TaskStatusCompleted {
			return false
		}
	}
	return true
}

// resultsAllFailed returns true if all task results have errors.
func resultsAllFailed(results []taskResult) bool {
	if len(results) == 0 {
		return true
	}
	for _, r := range results {
		if r.Err == nil {
			return false
		}
	}
	return true
}

// checkFileOverlaps warns about overlapping files within a phase.
// Tasks in the same phase run concurrently, so overlapping files
// may cause merge conflicts or race conditions.
func checkFileOverlaps(phase []Task) {
	fileOwners := make(map[string][]string)
	for _, t := range phase {
		for _, f := range t.Files {
			fileOwners[f] = append(fileOwners[f], t.ID)
		}
	}
	for f, owners := range fileOwners {
		if len(owners) > 1 {
			slog.Warn("file overlap detected within phase",
				"file", f,
				"tasks", strings.Join(owners, ", "),
			)
		}
	}
}

// runPhase executes all pending tasks in a phase with bounded parallelism.
func runPhase(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir, slug string,
	spec *Spec,
	phase []Task,
	maxParallel, maxRetries int,
) []taskResult {
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []taskResult
	)

	sem := make(chan struct{}, maxParallel)

	for _, task := range phase {
		// Skip already-completed or skipped tasks.
		if task.Status == TaskStatusCompleted || task.Status == TaskStatusSkipped {
			mu.Lock()
			results = append(results, taskResult{TaskID: task.ID, Title: task.Title})
			mu.Unlock()
			continue
		}

		wg.Add(1)
		go func(t Task) {
			defer wg.Done()

			// Acquire semaphore slot.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				results = append(results, taskResult{TaskID: t.ID, Title: t.Title, Err: ctx.Err()})
				mu.Unlock()
				return
			}

			result := executeWithRetry(ctx, client, cfg, repoDir, slug, spec, t, maxRetries)

			mu.Lock()
			results = append(results, result)
			mu.Unlock()
		}(task)
	}

	wg.Wait()
	return results
}

// executeWithRetry runs a task via RunTask, retrying on failure up to maxRetries times.
func executeWithRetry(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir, slug string,
	spec *Spec,
	task Task,
	maxRetries int,
) taskResult {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if ctx.Err() != nil {
			return taskResult{TaskID: task.ID, Title: task.Title, Err: ctx.Err(), Retries: attempt}
		}

		if attempt > 0 {
			slog.Info("retrying task", "task", task.ID, "attempt", attempt+1, "max_attempts", maxRetries+1)
			// Reset status to pending for retry — RunTask will mark it running.
			if err := UpdateTaskStatus(spec.TasksPath, task.ID, TaskStatusPending); err != nil {
				slog.Error("failed to reset task status for retry", "task", task.ID, "error", err)
				return taskResult{TaskID: task.ID, Title: task.Title, Err: lastErr, Retries: attempt}
			}
		}

		// Per-task timeout.
		taskCtx, cancel := context.WithTimeout(ctx, cfg.Spec.ParseTaskTimeout())

		var previousError string
		if lastErr != nil {
			previousError = lastErr.Error()
		}

		err := RunTask(taskCtx, client, cfg, repoDir, slug, task.ID, previousError)
		cancel()

		if err == nil {
			if attempt > 0 {
				slog.Info("task succeeded after retry", "task", task.ID, "attempts", attempt+1)
			}
			return taskResult{TaskID: task.ID, Title: task.Title, Retries: attempt}
		}

		lastErr = err
		slog.Warn("task failed", "task", task.ID, "attempt", attempt+1, "error", err)
	}

	return taskResult{TaskID: task.ID, Title: task.Title, Err: lastErr, Retries: maxRetries}
}

// reviewPhase runs the phase review gate using the secondary model.
// The secondary model reviews all uncommitted changes and fixes issues directly.
func reviewPhase(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	spec *Spec,
	phaseNum int,
) {
	if ctx.Err() != nil {
		return
	}

	diff, err := gitDiff(repoDir)
	if err != nil {
		slog.Warn("failed to get git diff for phase review", "error", err)
		return
	}

	if strings.TrimSpace(diff) == "" {
		slog.Debug("no changes to review", "phase", phaseNum)
		return
	}

	phaseSummaries := readPhaseSummaries(spec)

	data := map[string]string{
		"phase_summaries":     phaseSummaries,
		"uncommitted_changes": diff,
	}

	reviewPrompt, err := prompts.Execute("phase-review.md", data)
	if err != nil {
		slog.Warn("failed to render phase-review template", "error", err)
		return
	}

	slog.Info("running phase review gate", "phase", phaseNum)

	session, err := client.CreateSession(ctx, fmt.Sprintf("phase-%d-review", phaseNum), repoDir)
	if err != nil {
		slog.Warn("failed to create review session", "phase", phaseNum, "error", err)
		return
	}

	secondaryModel := opencode.ParseModelRef(cfg.Models.Secondary)
	_, err = client.SendPrompt(ctx, session.ID, reviewPrompt, secondaryModel, repoDir)
	if err != nil {
		slog.Warn("phase review failed", "phase", phaseNum, "error", err)
	}

	if err := client.DeleteSession(ctx, session.ID, repoDir); err != nil {
		slog.Warn("failed to delete review session", "error", err)
	}
}

// validateExternalAssumptions runs the External Assumption Validator & Repair agent.
// It examines all current changes for invalid, fragile, or unverifiable assumptions
// about external systems and fixes them in-place.
// Non-blocking: logs errors but does not fail execution.
func validateExternalAssumptions(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	spec *Spec,
	phaseNum int,
) {
	if ctx.Err() != nil {
		return
	}

	phaseSummaries := readPhaseSummaries(spec)

	data := map[string]string{
		"phase_summaries": phaseSummaries,
	}

	prompt, err := prompts.Execute("external-assumptions.md", data)
	if err != nil {
		slog.Warn("failed to render external-assumptions template", "error", err)
		return
	}

	slog.Info("running external assumption validation", "phase", phaseNum)

	session, err := client.CreateSession(ctx, fmt.Sprintf("phase-%d-ext-assumptions", phaseNum), repoDir)
	if err != nil {
		slog.Warn("failed to create external assumptions session", "phase", phaseNum, "error", err)
		return
	}

	primaryModel := opencode.ParseModelRef(cfg.Models.Primary)
	_, err = client.SendPrompt(ctx, session.ID, prompt, primaryModel, repoDir)
	if err != nil {
		slog.Warn("external assumption validation failed", "phase", phaseNum, "error", err)
	}

	if err := client.DeleteSession(ctx, session.ID, repoDir); err != nil {
		slog.Warn("failed to delete external assumptions session", "error", err)
	}
}

// hardenDomain runs the Domain Hardening & Polishing agent.
// It improves resilience, clarity, and operability of current changes,
// assuming external assumptions have already been validated.
// Non-blocking: logs errors but does not fail execution.
func hardenDomain(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	spec *Spec,
	phaseNum int,
) {
	if ctx.Err() != nil {
		return
	}

	phaseSummaries := readPhaseSummaries(spec)

	data := map[string]string{
		"phase_summaries": phaseSummaries,
	}

	prompt, err := prompts.Execute("domain-hardening.md", data)
	if err != nil {
		slog.Warn("failed to render domain-hardening template", "error", err)
		return
	}

	slog.Info("running domain hardening", "phase", phaseNum)

	session, err := client.CreateSession(ctx, fmt.Sprintf("phase-%d-domain-hardening", phaseNum), repoDir)
	if err != nil {
		slog.Warn("failed to create domain hardening session", "phase", phaseNum, "error", err)
		return
	}

	primaryModel := opencode.ParseModelRef(cfg.Models.Primary)
	_, err = client.SendPrompt(ctx, session.ID, prompt, primaryModel, repoDir)
	if err != nil {
		slog.Warn("domain hardening failed", "phase", phaseNum, "error", err)
	}

	if err := client.DeleteSession(ctx, session.ID, repoDir); err != nil {
		slog.Warn("failed to delete domain hardening session", "error", err)
	}
}

// alignDocumentation runs the Documentation Alignment agent with multi-model review.
// It ensures documentation accurately reflects the current behavior of the branch
// after all task phases, fixes, and hardening have been applied.
// This runs once after all phases complete, using the ReviewPipeline for quality.
// Non-blocking: logs errors but does not fail execution.
func alignDocumentation(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	spec *Spec,
) {
	if ctx.Err() != nil {
		return
	}

	phaseSummaries := readPhaseSummaries(spec)

	data := map[string]string{
		"phase_summaries": phaseSummaries,
	}

	prompt, err := prompts.Execute("documentation-alignment.md", data)
	if err != nil {
		slog.Warn("failed to render documentation-alignment template", "error", err)
		return
	}

	slog.Info("running documentation alignment with multi-model review")

	// Use the ReviewPipeline for multi-model quality assurance.
	primaryModel := opencode.ParseModelRef(cfg.Models.Primary)
	secondaryModel := opencode.ParseModelRef(cfg.Models.Secondary)

	reviewCfg := opencode.ReviewConfig{
		Primary:   primaryModel,
		Secondary: secondaryModel,
		MaxCycles: 1,
	}
	if cfg.Models.Tertiary != "" {
		tertiaryModel := opencode.ParseModelRef(cfg.Models.Tertiary)
		reviewCfg.Tertiary = &tertiaryModel
	}

	pipeline := opencode.NewReviewPipeline(client, repoDir, reviewCfg)
	_, err = pipeline.Review(ctx, prompt, data)
	if err != nil {
		slog.Warn("documentation alignment review failed", "error", err)
		return
	}

	// Commit documentation changes separately if any were made.
	if gitHasChanges(repoDir) {
		if err := gitCommit(repoDir, "otto: documentation alignment"); err != nil {
			slog.Warn("failed to commit documentation alignment changes", "error", err)
		} else {
			slog.Info("committed documentation alignment changes")
		}
	} else {
		slog.Info("no documentation changes needed")
	}
}

// commitPhase stages all changes and commits for a phase.
// Returns true if a commit was made, false if there were no changes.
func commitPhase(repoDir string, phaseNum int, phase []Task) bool {
	if !gitHasChanges(repoDir) {
		slog.Info("no changes to commit", "phase", phaseNum)
		return false
	}

	// Build commit message from task titles.
	var titles []string
	for _, t := range phase {
		titles = append(titles, t.Title)
	}
	summary := strings.Join(titles, ", ")
	if len(summary) > 72 {
		summary = summary[:69] + "..."
	}

	message := fmt.Sprintf("otto: phase %d — %s", phaseNum, summary)

	if err := gitCommit(repoDir, message); err != nil {
		slog.Error("failed to commit phase", "phase", phaseNum, "error", err)
		return false
	}

	slog.Info("committed phase", "phase", phaseNum)
	return true
}

// generatePhaseSummary creates and persists a phase summary for summary chaining.
// Writes to history/phase-N-summary.md matching the readPhaseSummaries convention.
func generatePhaseSummary(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	spec *Spec,
	phaseNum int,
	phase []Task,
) {
	if ctx.Err() != nil {
		return
	}

	var buf strings.Builder
	buf.WriteString("Summarize the following completed phase in 2-3 paragraphs. ")
	buf.WriteString("Focus on what was accomplished, key decisions made, and any notable details.\n\n")
	buf.WriteString(fmt.Sprintf("## Phase %d Tasks\n\n", phaseNum))
	for _, t := range phase {
		buf.WriteString(fmt.Sprintf("- **%s**: %s\n", t.Title, t.Description))
	}

	session, err := client.CreateSession(ctx, fmt.Sprintf("phase-%d-summary", phaseNum), repoDir)
	if err != nil {
		slog.Warn("failed to create summary session", "phase", phaseNum, "error", err)
		return
	}

	primaryModel := opencode.ParseModelRef(cfg.Models.Primary)
	resp, err := client.SendPrompt(ctx, session.ID, buf.String(), primaryModel, repoDir)
	if err != nil {
		slog.Warn("failed to generate phase summary", "phase", phaseNum, "error", err)
		_ = client.DeleteSession(ctx, session.ID, repoDir)
		return
	}

	_ = client.DeleteSession(ctx, session.ID, repoDir)

	// Write summary to history/phase-N-summary.md (matches readPhaseSummaries convention).
	summaryPath := filepath.Join(spec.HistoryDir, fmt.Sprintf("phase-%d-summary.md", phaseNum))
	if err := store.WriteBody(summaryPath, resp.Content); err != nil {
		slog.Warn("failed to write phase summary", "phase", phaseNum, "error", err)
	} else {
		slog.Info("saved phase summary", "phase", phaseNum, "path", summaryPath)
	}
}

// harvestQuestions scans dialog logs from the phase for uncertainties and questions.
// Questions are appended to the spec's questions.md file.
// Non-blocking: logs errors but continues execution.
func harvestQuestions(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	spec *Spec,
	phaseNum int,
	historyBaseline int,
) {
	if ctx.Err() != nil {
		return
	}

	logs := collectPhaseLogs(spec.HistoryDir, historyBaseline)
	if logs == "" {
		slog.Debug("no execution logs to harvest questions from", "phase", phaseNum)
		return
	}

	phaseSummaries := readPhaseSummaries(spec)

	data := map[string]string{
		"execution_logs":  logs,
		"phase_summaries": phaseSummaries,
		"phase_number":    fmt.Sprintf("%d", phaseNum),
	}

	harvestPrompt, err := prompts.Execute("question-harvest.md", data)
	if err != nil {
		slog.Warn("failed to render question-harvest template", "error", err)
		return
	}

	session, err := client.CreateSession(ctx, fmt.Sprintf("phase-%d-harvest", phaseNum), repoDir)
	if err != nil {
		slog.Warn("failed to create harvest session", "error", err)
		return
	}

	primaryModel := opencode.ParseModelRef(cfg.Models.Primary)
	resp, err := client.SendPrompt(ctx, session.ID, harvestPrompt, primaryModel, repoDir)
	if err != nil {
		slog.Warn("question harvesting failed", "phase", phaseNum, "error", err)
		_ = client.DeleteSession(ctx, session.ID, repoDir)
		return
	}

	_ = client.DeleteSession(ctx, session.ID, repoDir)

	content := strings.TrimSpace(resp.Content)
	if content == "" || strings.Contains(content, "No questions identified") {
		slog.Debug("no questions harvested", "phase", phaseNum)
		return
	}

	existing := readArtifact(spec.QuestionsPath)
	updated := existing
	if updated != "" {
		updated += "\n\n"
	}
	updated += fmt.Sprintf("<!-- Phase %d harvest -->\n%s", phaseNum, content)

	if err := store.WriteBody(spec.QuestionsPath, updated); err != nil {
		slog.Warn("failed to write harvested questions", "error", err)
	} else {
		slog.Info("harvested questions", "phase", phaseNum)
	}
}

// collectPhaseLogs reads history files created after the given baseline number.
func collectPhaseLogs(historyDir string, baseline int) string {
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		return ""
	}

	var logs strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := historyNumRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		num, _ := strconv.Atoi(m[1])
		if num <= baseline {
			continue
		}
		content, err := os.ReadFile(filepath.Join(historyDir, e.Name()))
		if err != nil {
			continue
		}
		logs.WriteString(fmt.Sprintf("### %s\n\n%s\n\n", e.Name(), string(content)))
	}

	return logs.String()
}

// --- Git helpers ---

// gitDiff returns uncommitted changes relative to HEAD.
func gitDiff(repoDir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "diff", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		// If HEAD doesn't exist (no commits yet), fall back to plain diff.
		cmd2 := exec.CommandContext(ctx, "git", "diff")
		cmd2.Dir = repoDir
		out2, err2 := cmd2.Output()
		if err2 != nil {
			return "", fmt.Errorf("git diff: %w", err)
		}
		return string(out2), nil
	}
	return string(out), nil
}

// gitHasChanges returns true if the working directory has uncommitted changes.
func gitHasChanges(repoDir string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		slog.Warn("git status failed, assuming no changes", "error", err)
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// gitCommit stages all changes and commits with the given message.
func gitCommit(repoDir string, message string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	add := exec.CommandContext(ctx, "git", "add", "-A")
	add.Dir = repoDir
	if out, err := add.CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %s: %w", string(out), err)
	}

	commit := exec.CommandContext(ctx, "git", "commit", "-m", message)
	commit.Dir = repoDir
	if out, err := commit.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %s: %w", string(out), err)
	}

	return nil
}

// --- Progress display helpers ---

// printPhaseHeader prints a styled phase header to stderr.
func printPhaseHeader(phaseNum int, phase []Task) {
	style := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12"))

	pending := 0
	for _, t := range phase {
		if t.Status != TaskStatusCompleted {
			pending++
		}
	}

	header := fmt.Sprintf("▶ Phase %d — %d task(s), %d pending", phaseNum, len(phase), pending)
	fmt.Fprintf(os.Stderr, "\n%s\n", style.Render(header))
}

// printPhaseResults prints task results after a phase completes.
func printPhaseResults(phaseNum int, results []taskResult) {
	successStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	failStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))

	for _, r := range results {
		if r.Err != nil {
			msg := fmt.Sprintf("  ✗ %s — %s (retries: %d, error: %s)", r.TaskID, r.Title, r.Retries, r.Err)
			fmt.Fprintln(os.Stderr, failStyle.Render(msg))
		} else {
			msg := fmt.Sprintf("  ✓ %s — %s", r.TaskID, r.Title)
			if r.Retries > 0 {
				msg += fmt.Sprintf(" (succeeded after %d retries)", r.Retries)
			}
			fmt.Fprintln(os.Stderr, successStyle.Render(msg))
		}
	}
}

// printOverallProgress prints overall execution progress to stderr.
func printOverallProgress(completedPhases, totalPhases int, tasks []Task) {
	style := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("14"))

	var completed, pending, failed int
	for _, t := range tasks {
		switch t.Status {
		case TaskStatusCompleted:
			completed++
		case TaskStatusPending:
			pending++
		case TaskStatusFailed:
			failed++
		}
	}

	total := len(tasks)
	pct := 0
	if total > 0 {
		pct = completed * 100 / total
	}

	progress := fmt.Sprintf(
		"Progress: phase %d/%d | %d/%d tasks complete (%d%%) | %d pending, %d failed",
		completedPhases, totalPhases, completed, total, pct, pending, failed,
	)
	fmt.Fprintf(os.Stderr, "\n%s\n", style.Render(progress))
}
