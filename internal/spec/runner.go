package spec

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/opencode"
	"github.com/alanmeadows/otto/internal/prompts"
	"github.com/alanmeadows/otto/internal/store"
)

// RunTask executes a single task from a spec's tasks.md.
// If taskID is empty, it infers the task when exactly one is runnable.
// previousError, if non-empty, is the error message from a prior failed attempt
// and will be injected into the task prompt so the LLM can correct the issue.
func RunTask(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	slug string,
	taskID string,
	previousError string,
) error {
	spec, err := ResolveSpec(slug, repoDir)
	if err != nil {
		return err
	}

	if err := CheckPrerequisites(spec, "execute"); err != nil {
		return err
	}

	tasks, err := ParseTasks(spec.TasksPath)
	if err != nil {
		return fmt.Errorf("parsing tasks: %w", err)
	}

	var task *Task

	if taskID == "" {
		// Infer the task from runnable tasks.
		runnable := GetRunnableTasks(tasks)
		switch len(runnable) {
		case 0:
			return fmt.Errorf("no runnable tasks")
		case 1:
			task = &runnable[0]
			slog.Info("inferred task", "id", task.ID, "title", task.Title)
		default:
			ids := make([]string, len(runnable))
			for i, r := range runnable {
				ids[i] = r.ID
			}
			return fmt.Errorf("multiple runnable tasks: %s — specify --id", strings.Join(ids, ", "))
		}
	} else {
		// Find by ID.
		for i := range tasks {
			if tasks[i].ID == taskID {
				task = &tasks[i]
				break
			}
		}
		if task == nil {
			return fmt.Errorf("task %q not found", taskID)
		}
	}

	// Mark as running.
	if err := UpdateTaskStatus(spec.TasksPath, task.ID, TaskStatusRunning); err != nil {
		return fmt.Errorf("marking task as running: %w", err)
	}

	fmt.Fprintf(os.Stderr, "  ▶ %s: %s\n", task.ID, task.Title)

	// Ensure OpenCode permissions.
	if err := opencode.EnsurePermissions(repoDir); err != nil {
		return fmt.Errorf("ensuring permissions: %w", err)
	}

	// Create session scoped to the repo directory.
	session, err := client.CreateSession(ctx, fmt.Sprintf("task-%s", task.ID), repoDir)
	if err != nil {
		_ = UpdateTaskStatus(spec.TasksPath, task.ID, TaskStatusFailed)
		return fmt.Errorf("creating session: %w", err)
	}
	defer func() {
		if err := client.DeleteSession(ctx, session.ID, repoDir); err != nil {
			slog.Warn("failed to delete session", "error", err)
		}
	}()

	// Build task execution prompt.
	var prompt string
	if cfg.Spec.IsTaskBriefingEnabled() {
		fmt.Fprintf(os.Stderr, "    ⏳ Generating task briefing...\n")
		slog.Info("generating task briefing", "task", task.ID)
		brief, briefErr := briefTask(ctx, client, cfg, repoDir, spec, task)
		if briefErr != nil {
			slog.Warn("task briefing failed, falling back to static prompt", "task", task.ID, "error", briefErr)
			prompt = buildTaskPrompt(spec, task, previousError)
		} else {
			prompt = buildBriefedPrompt(spec, task, brief, previousError)
		}
	} else {
		prompt = buildTaskPrompt(spec, task, previousError)
	}

	primaryModel := opencode.ParseModelRef(cfg.Models.Primary)

	// Send prompt and wait for completion.
	fmt.Fprintf(os.Stderr, "    ⏳ Executing task (this may take several minutes)...\n")
	_, err = client.SendPrompt(ctx, session.ID, prompt, primaryModel, repoDir)
	if err != nil {
		_ = UpdateTaskStatus(spec.TasksPath, task.ID, TaskStatusFailed)
		return fmt.Errorf("executing task: %w", err)
	}

	// Get dialog log and save to history.
	messages, err := client.GetMessages(ctx, session.ID, repoDir)
	if err != nil {
		slog.Warn("failed to retrieve messages for history", "error", err)
	} else {
		// Ensure history dir exists.
		if err := os.MkdirAll(spec.HistoryDir, 0755); err != nil {
			slog.Warn("failed to create history directory", "error", err)
		} else {
			num := nextHistoryNumber(spec.HistoryDir)
			if err := saveHistory(spec.HistoryDir, num, task.ID, messages); err != nil {
				slog.Warn("failed to save history", "error", err)
			} else {
				slog.Info("saved run history", "file", fmt.Sprintf("run-%03d-%s.md", num, task.ID))
			}
		}
	}

	// Mark as completed.
	if err := UpdateTaskStatus(spec.TasksPath, task.ID, TaskStatusCompleted); err != nil {
		return fmt.Errorf("marking task as completed: %w", err)
	}

	slog.Info("task completed", "id", task.ID, "title", task.Title)
	return nil
}

// buildTaskPrompt builds the composite prompt for task execution.
// If previousError is non-empty, it is included so the LLM can fix the prior failure.
func buildTaskPrompt(spec *Spec, task *Task, previousError string) string {
	var buf strings.Builder

	buf.WriteString("You are executing a development task. Here is the context:\n\n")

	// Requirements.
	requirementsMD := readArtifact(spec.RequirementsPath)
	if requirementsMD != "" {
		buf.WriteString("## Requirements\n\n")
		buf.WriteString(requirementsMD)
		buf.WriteString("\n\n")
	}

	// Research.
	researchMD := readArtifact(spec.ResearchPath)
	if researchMD != "" {
		buf.WriteString("## Research\n\n")
		buf.WriteString(researchMD)
		buf.WriteString("\n\n")
	}

	// Design.
	designMD := readArtifact(spec.DesignPath)
	if designMD != "" {
		buf.WriteString("## Design\n\n")
		buf.WriteString(designMD)
		buf.WriteString("\n\n")
	}

	// Task description.
	buf.WriteString("## Your Task\n\n")
	buf.WriteString(fmt.Sprintf("**Task ID**: %s\n", task.ID))
	buf.WriteString(fmt.Sprintf("**Title**: %s\n", task.Title))
	buf.WriteString(fmt.Sprintf("**Description**: %s\n", task.Description))
	if len(task.Files) > 0 {
		buf.WriteString(fmt.Sprintf("**Files**: %s\n", strings.Join(task.Files, ", ")))
	}
	buf.WriteString("\n")

	// Phase summaries.
	phaseSummaries := readPhaseSummaries(spec)
	if phaseSummaries != "" {
		buf.WriteString("## Prior Phase Summaries\n\n")
		buf.WriteString(phaseSummaries)
		buf.WriteString("\n\n")
	}

	// Previous error context for retries.
	if previousError != "" {
		buf.WriteString("## Previous Attempt Failed\n\n")
		buf.WriteString("The previous attempt at this task failed with the following error. Fix the issue and complete the task:\n\n")
		buf.WriteString(previousError)
		buf.WriteString("\n\n")
	}

	buf.WriteString("Complete this task. Make all necessary code changes.\n")

	return buf.String()
}

// briefTask generates a focused implementation brief for a task using an LLM call.
// It reads all spec artifacts, renders the task-briefing.md template, and sends the
// prompt to the primary model. The returned brief replaces the verbose context dump
// that buildTaskPrompt produces, giving the executor a distilled, task-specific prompt.
//
// The brief includes pointers back to requirements.md, design.md, etc. so the executor
// can explore further if needed.
func briefTask(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	spec *Spec,
	task *Task,
) (string, error) {
	requirementsMD := readArtifact(spec.RequirementsPath)
	researchMD := readArtifact(spec.ResearchPath)
	designMD := readArtifact(spec.DesignPath)
	tasksMD := readArtifact(spec.TasksPath)
	phaseSummaries := readPhaseSummaries(spec)

	data := map[string]string{
		"requirements_md":  requirementsMD,
		"research_md":      researchMD,
		"design_md":        designMD,
		"tasks_md":         tasksMD,
		"task_id":          task.ID,
		"task_title":       task.Title,
		"task_description": task.Description,
	}

	if len(task.Files) > 0 {
		data["task_files"] = strings.Join(task.Files, ", ")
	}

	if len(task.DependsOn) > 0 {
		data["task_depends_on"] = strings.Join(task.DependsOn, ", ")
	}

	if phaseSummaries != "" {
		data["phase_summaries"] = phaseSummaries
	}

	rendered, err := prompts.Execute("task-briefing.md", data)
	if err != nil {
		return "", fmt.Errorf("rendering task-briefing template: %w", err)
	}

	session, err := client.CreateSession(ctx, fmt.Sprintf("brief-%s", task.ID), repoDir)
	if err != nil {
		return "", fmt.Errorf("creating briefing session: %w", err)
	}
	defer func() {
		if delErr := client.DeleteSession(ctx, session.ID, repoDir); delErr != nil {
			slog.Warn("failed to delete briefing session", "error", delErr)
		}
	}()

	primaryModel := opencode.ParseModelRef(cfg.Models.Primary)
	resp, err := client.SendPrompt(ctx, session.ID, rendered, primaryModel, repoDir)
	if err != nil {
		return "", fmt.Errorf("briefing LLM call: %w", err)
	}

	brief := strings.TrimSpace(resp.Content)
	if brief == "" {
		return "", fmt.Errorf("briefing LLM returned empty response")
	}

	slog.Info("generated task briefing", "task", task.ID, "brief_length", len(brief))
	return brief, nil
}

// buildBriefedPrompt wraps a task briefing into the final executor prompt.
// It includes the brief plus pointers to the spec artifacts and any previous error context.
func buildBriefedPrompt(spec *Spec, task *Task, brief string, previousError string) string {
	var buf strings.Builder

	buf.WriteString("You are executing a development task. A senior engineer has prepared a detailed implementation brief for you.\n\n")

	buf.WriteString("## Implementation Brief\n\n")
	buf.WriteString(brief)
	buf.WriteString("\n\n")

	buf.WriteString("## Reference Documents\n\n")
	buf.WriteString("If you need additional context beyond this brief, the following spec documents are available in the repository:\n\n")
	buf.WriteString(fmt.Sprintf("- **Requirements**: `%s`\n", spec.RequirementsPath))
	buf.WriteString(fmt.Sprintf("- **Research**: `%s`\n", spec.ResearchPath))
	buf.WriteString(fmt.Sprintf("- **Design**: `%s`\n", spec.DesignPath))
	buf.WriteString(fmt.Sprintf("- **Tasks**: `%s`\n", spec.TasksPath))
	buf.WriteString("\nRead these files if you need deeper context on requirements, design decisions, or adjacent tasks.\n\n")

	// Previous error context for retries.
	if previousError != "" {
		buf.WriteString("## Previous Attempt Failed\n\n")
		buf.WriteString("The previous attempt at this task failed with the following error. Fix the issue and complete the task:\n\n")
		buf.WriteString(previousError)
		buf.WriteString("\n\n")
	}

	buf.WriteString("Complete this task. Make all necessary code changes.\n")

	return buf.String()
}

// historyNumRe matches run-NNN.md and run-NNN-TASKID.md filenames.
var historyNumRe = regexp.MustCompile(`^run-(\d+)(?:-[a-zA-Z0-9_-]+)?\.md$`)

// nextHistoryNumber scans the history directory for run-NNN.md files
// and returns the next sequential number.
func nextHistoryNumber(historyDir string) int {
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		return 1
	}

	maxNum := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := historyNumRe.FindStringSubmatch(e.Name())
		if m != nil {
			n, _ := strconv.Atoi(m[1])
			if n > maxNum {
				maxNum = n
			}
		}
	}

	return maxNum + 1
}

// saveHistory writes a run history file with all messages from a session.
// The taskID is included in the filename to avoid collisions during parallel execution.
func saveHistory(historyDir string, num int, taskID string, messages []opencode.Message) error {
	filename := fmt.Sprintf("run-%03d-%s.md", num, taskID)
	path := filepath.Join(historyDir, filename)

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("# Run %03d\n\n", num))

	for _, msg := range messages {
		buf.WriteString(fmt.Sprintf("## %s\n\n", msg.Role))
		buf.WriteString(msg.Content)
		buf.WriteString("\n\n---\n\n")
	}

	return store.WriteBody(path, buf.String())
}
