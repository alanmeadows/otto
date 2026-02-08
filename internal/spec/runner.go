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
	"github.com/alanmeadows/otto/internal/store"
)

// RunTask executes a single task from a spec's tasks.md.
// If taskID is empty, it infers the task when exactly one is runnable.
func RunTask(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	slug string,
	taskID string,
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

	// Build task execution prompt.
	prompt := buildTaskPrompt(spec, task)

	primaryModel := opencode.ParseModelRef(cfg.Models.Primary)

	// Send prompt and wait for completion.
	_, err = client.SendPrompt(ctx, session.ID, prompt, primaryModel, repoDir)
	if err != nil {
		_ = UpdateTaskStatus(spec.TasksPath, task.ID, TaskStatusFailed)
		_ = client.DeleteSession(ctx, session.ID, repoDir)
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
			if err := saveHistory(spec.HistoryDir, num, messages); err != nil {
				slog.Warn("failed to save history", "error", err)
			} else {
				slog.Info("saved run history", "file", fmt.Sprintf("run-%03d.md", num))
			}
		}
	}

	// Mark as completed.
	if err := UpdateTaskStatus(spec.TasksPath, task.ID, TaskStatusCompleted); err != nil {
		return fmt.Errorf("marking task as completed: %w", err)
	}

	// Delete session.
	if err := client.DeleteSession(ctx, session.ID, repoDir); err != nil {
		slog.Warn("failed to delete session", "error", err)
	}

	slog.Info("task completed", "id", task.ID, "title", task.Title)
	return nil
}

// buildTaskPrompt builds the composite prompt for task execution.
func buildTaskPrompt(spec *Spec, task *Task) string {
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

	buf.WriteString("Complete this task. Make all necessary code changes.\n")

	return buf.String()
}

// historyNumRe matches run-NNN.md filenames.
var historyNumRe = regexp.MustCompile(`^run-(\d+)\.md$`)

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
func saveHistory(historyDir string, num int, messages []opencode.Message) error {
	filename := fmt.Sprintf("run-%03d.md", num)
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
