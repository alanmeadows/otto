package spec

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/opencode"
	"github.com/alanmeadows/otto/internal/prompts"
	"github.com/alanmeadows/otto/internal/store"
)

// maxTaskGenRetries is the number of format-correction retries for task generation.
const maxTaskGenRetries = 2

// SpecTaskGenerate generates or refines the tasks document for a spec.
func SpecTaskGenerate(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	slug string,
	force bool,
) error {
	spec, err := ResolveSpec(slug, repoDir)
	if err != nil {
		return err
	}

	if err := CheckPrerequisites(spec, "task-generate"); err != nil {
		return err
	}

	// Question gating: check for unanswered questions from prior phases.
	if !force {
		unanswered, err := CheckUnansweredQuestions(spec)
		if err != nil {
			return err
		}
		if unanswered > 0 {
			return fmt.Errorf("%d unanswered question(s) — run 'otto spec questions' to resolve, or re-run with --force to skip", unanswered)
		}
	}

	// Read all existing artifacts
	requirementsMD := readArtifact(spec.RequirementsPath)
	researchMD := readArtifact(spec.ResearchPath)
	designMD := readArtifact(spec.DesignPath)
	existingTasksMD := readArtifact(spec.TasksPath)
	questionsMD := readArtifact(spec.QuestionsPath)

	summary, err := AnalyzeCodebase(repoDir)
	if err != nil {
		return fmt.Errorf("analyzing codebase: %w", err)
	}

	// Build data map for template
	data := map[string]string{
		"requirements_md":  requirementsMD,
		"research_md":      researchMD,
		"design_md":        designMD,
		"codebase_summary": summary.String(),
	}
	if existingTasksMD != "" {
		data["existing_tasks_md"] = existingTasksMD
	}
	if questionsMD != "" {
		data["questions_md"] = questionsMD
	}

	// Gather phase summaries from the history directory if they exist
	phaseSummaries := readPhaseSummaries(spec)
	if phaseSummaries != "" {
		data["phase_summaries"] = phaseSummaries
	}

	rendered, err := prompts.Execute("tasks.md", data)
	if err != nil {
		return fmt.Errorf("rendering tasks prompt: %w", err)
	}

	pipeline := buildReviewPipeline(client, repoDir, cfg)

	contextData := map[string]string{
		"Requirements": requirementsMD,
		"Research":     researchMD,
		"Design":       designMD,
	}
	if summary.String() != "" {
		contextData["Codebase Summary"] = summary.String()
	}

	result, stats, err := pipeline.Review(ctx, rendered, contextData)
	if err != nil {
		return fmt.Errorf("review pipeline: %w", err)
	}

	printReviewStats("Task generation", stats)

	// Split questions from tasks output before writing.
	tasksContent, _ := SplitQuestions(result)

	// Validate structured task format BEFORE writing to disk.
	tasks, validationErr := validateTaskOutput(tasksContent)
	if validationErr != nil {
		slog.Warn("task output failed validation, retrying with format correction", "error", validationErr)

		primaryModel := opencode.ParseModelRef(cfg.Models.Primary)
		for attempt := 1; attempt <= maxTaskGenRetries; attempt++ {
			fmt.Fprintf(os.Stderr, "  ⚠ Task output invalid (%s), retry %d/%d...\n", validationErr, attempt, maxTaskGenRetries)

			corrected, retryErr := retryTaskGeneration(ctx, client, primaryModel, repoDir, tasksContent, validationErr)
			if retryErr != nil {
				slog.Warn("retry failed", "attempt", attempt, "error", retryErr)
				continue
			}

			correctedContent, _ := SplitQuestions(corrected)
			tasks, validationErr = validateTaskOutput(correctedContent)
			if validationErr == nil {
				tasksContent = correctedContent
				result = corrected // for question extraction below
				slog.Info("retry produced valid tasks", "attempt", attempt)
				break
			}
			slog.Warn("retry output still invalid", "attempt", attempt, "error", validationErr)
		}

		if validationErr != nil {
			return fmt.Errorf("task generation failed after %d retries: %w", maxTaskGenRetries, validationErr)
		}
	}

	if err := store.WriteBody(spec.TasksPath, tasksContent); err != nil {
		return fmt.Errorf("writing tasks: %w", err)
	}

	// Extract and append questions from the output.
	ExtractAndAppendQuestions(result, spec)

	slog.Info("tasks generated", "spec", spec.Slug, "task_count", len(tasks))

	// Auto-resolve any new questions.
	ResolveAndReport(ctx, client, cfg, repoDir, spec, "task-generate")

	return nil
}

// SpecTaskAdd adds a new task to an existing tasks.md using a single LLM call.
// It does NOT use the review pipeline — just sends the current tasks.md plus the
// user's prompt to the primary model and writes the result.
func SpecTaskAdd(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	slug string,
	prompt string,
) error {
	spec, err := ResolveSpec(slug, repoDir)
	if err != nil {
		return err
	}

	// Read existing tasks content.
	existingTasks := readArtifact(spec.TasksPath)

	// Build a simple prompt for the LLM.
	llmPrompt := fmt.Sprintf(`Here is the current tasks.md:

%s

Please add this new task: %s

Return the COMPLETE updated tasks.md with the new task inserted in the correct position, with appropriate ID, parallel_group, and depends_on set. Output ONLY the tasks.md content — no preamble, no commentary.`, existingTasks, prompt)

	primaryModel := opencode.ParseModelRef(cfg.Models.Primary)

	// Create session, send prompt.
	session, err := client.CreateSession(ctx, "task-add", repoDir)
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	resp, err := client.SendPrompt(ctx, session.ID, llmPrompt, primaryModel, repoDir)
	if err != nil {
		_ = client.DeleteSession(ctx, session.ID, repoDir)
		return fmt.Errorf("sending prompt: %w", err)
	}

	content := resp.Content

	// Validate structured task format BEFORE writing to disk.
	tasks, validationErr := validateTaskOutput(content)
	if validationErr != nil {
		slog.Warn("task-add output failed validation, retrying", "error", validationErr)

		for attempt := 1; attempt <= maxTaskGenRetries; attempt++ {
			fmt.Fprintf(os.Stderr, "  ⚠ Task output invalid (%s), retry %d/%d...\n", validationErr, attempt, maxTaskGenRetries)

			corrected, retryErr := retryTaskGeneration(ctx, client, primaryModel, repoDir, content, validationErr)
			if retryErr != nil {
				slog.Warn("retry failed", "attempt", attempt, "error", retryErr)
				continue
			}

			tasks, validationErr = validateTaskOutput(corrected)
			if validationErr == nil {
				content = corrected
				slog.Info("retry produced valid tasks", "attempt", attempt)
				break
			}
			slog.Warn("retry output still invalid", "attempt", attempt, "error", validationErr)
		}

		if validationErr != nil {
			_ = client.DeleteSession(ctx, session.ID, repoDir)
			return fmt.Errorf("task-add failed after %d retries: %w", maxTaskGenRetries, validationErr)
		}
	}

	// Write validated result with file locking.
	if err := store.WithLock(spec.TasksPath, 5*time.Second, func() error {
		return store.WriteBody(spec.TasksPath, content)
	}); err != nil {
		_ = client.DeleteSession(ctx, session.ID, repoDir)
		return fmt.Errorf("writing tasks: %w", err)
	}

	slog.Info("task added", "spec", spec.Slug, "task_count", len(tasks))

	// Cleanup session.
	_ = client.DeleteSession(ctx, session.ID, repoDir)
	return nil
}

// validateTaskOutput parses the content as structured tasks and returns an error
// if the output is empty, contains no valid tasks, or has parsing issues.
func validateTaskOutput(content string) ([]Task, error) {
	tasks, err := ParseTasksFromString(content)
	if err != nil {
		return nil, fmt.Errorf("invalid task format: %w", err)
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no structured tasks found — output must contain '## Task N:' headers with id, status, parallel_group, depends_on, description, and files fields")
	}
	return tasks, nil
}

// retryTaskGeneration sends a format-correction prompt to the LLM with the
// invalid output and the validation error, asking for properly structured tasks.
func retryTaskGeneration(
	ctx context.Context,
	client opencode.LLMClient,
	model opencode.ModelRef,
	directory string,
	invalidOutput string,
	validationErr error,
) (string, error) {
	session, err := client.CreateSession(ctx, "task-format-retry", directory)
	if err != nil {
		return "", fmt.Errorf("creating retry session: %w", err)
	}
	defer client.DeleteSession(ctx, session.ID, directory)

	prompt := fmt.Sprintf(`Your previous output was not in the required structured task format.

Validation error: %s

Your output was:
---
%s
---

Please rewrite the output as structured tasks using this EXACT format:

# Tasks

## Task 1: <concise title>
- **id**: task-001
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: <detailed implementation instructions>
- **files**: ["path/to/file.go"]

Rules:
- Every task MUST have a "## Task N:" header
- Every task MUST have all six fields: id, status, parallel_group, depends_on, description, files
- Do NOT output the content of files that tasks will create — describe what to build, not the file content itself
- Output ONLY the structured task list — no preamble, no commentary

CRITICAL: Return ALL output directly in your response text. Do NOT use any file editing tools.`,
		validationErr, invalidOutput)

	resp, err := client.SendPrompt(ctx, session.ID, prompt, model, directory)
	if err != nil {
		return "", fmt.Errorf("retry prompt: %w", err)
	}
	return resp.Content, nil
}

// readPhaseSummaries reads any phase summary files from the spec's history directory.
func readPhaseSummaries(spec *Spec) string {
	if !store.Exists(spec.HistoryDir) {
		return ""
	}

	// Look for phase-N-summary.md files
	var summaries string
	const maxPhases = 100 // generous upper bound
	for i := 1; i <= maxPhases; i++ {
		path := fmt.Sprintf("%s/phase-%d-summary.md", spec.HistoryDir, i)
		if store.Exists(path) {
			content := readArtifact(path)
			if content != "" {
				summaries += fmt.Sprintf("### Phase %d Summary\n\n%s\n\n", i, content)
			}
		}
	}
	return summaries
}
