package spec

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/opencode"
	"github.com/alanmeadows/otto/internal/prompts"
	"github.com/alanmeadows/otto/internal/store"
)

// SpecTaskGenerate generates or refines the tasks document for a spec.
func SpecTaskGenerate(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	slug string,
) error {
	spec, err := ResolveSpec(slug, repoDir)
	if err != nil {
		return err
	}

	if err := CheckPrerequisites(spec, "task-generate"); err != nil {
		return err
	}

	// Read all existing artifacts
	requirementsMD := readArtifact(spec.RequirementsPath)
	researchMD := readArtifact(spec.ResearchPath)
	designMD := readArtifact(spec.DesignPath)
	existingTasksMD := readArtifact(spec.TasksPath)

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

	result, err := pipeline.Review(ctx, rendered, contextData)
	if err != nil {
		return fmt.Errorf("review pipeline: %w", err)
	}

	if err := store.WriteBody(spec.TasksPath, result); err != nil {
		return fmt.Errorf("writing tasks: %w", err)
	}

	// Validate task structure by attempting to parse
	tasks, parseErr := ParseTasks(spec.TasksPath)
	if parseErr != nil {
		slog.Warn("generated tasks have parsing issues — review tasks.md manually", "error", parseErr)
	} else {
		slog.Info("tasks generated", "spec", spec.Slug, "task_count", len(tasks))
	}

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

	// Write result with file locking.
	if err := store.WithLock(spec.TasksPath, 5*time.Second, func() error {
		return store.WriteBody(spec.TasksPath, resp.Content)
	}); err != nil {
		_ = client.DeleteSession(ctx, session.ID, repoDir)
		return fmt.Errorf("writing tasks: %w", err)
	}

	// Validate by parsing.
	tasks, parseErr := ParseTasks(spec.TasksPath)
	if parseErr != nil {
		slog.Warn("updated tasks have parsing issues — review tasks.md manually", "error", parseErr)
	} else {
		slog.Info("task added", "spec", spec.Slug, "task_count", len(tasks))
	}

	// Cleanup session.
	_ = client.DeleteSession(ctx, session.ID, repoDir)
	return nil
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
