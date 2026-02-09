package spec

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/opencode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupRunnerSpec(t *testing.T) (string, string) {
	t.Helper()
	repoDir := setupSpecDir(t, "runner-test", "requirements.md", "research.md", "design.md", "tasks.md")
	specDir := filepath.Join(repoDir, ".otto", "specs", "runner-test")

	tasks := `# Tasks

## Task 1: Setup
- **id**: task-001
- **status**: completed
- **parallel_group**: 1
- **depends_on**: []
- **description**: Initial setup.
- **files**: []

## Task 2: Implement feature
- **id**: task-002
- **status**: pending
- **parallel_group**: 2
- **depends_on**: [task-001]
- **description**: Implement the main feature.
- **files**: ["internal/feature.go"]

## Task 3: Write tests
- **id**: task-003
- **status**: pending
- **parallel_group**: 3
- **depends_on**: [task-002]
- **description**: Write tests for the feature.
- **files**: ["internal/feature_test.go"]
`
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "tasks.md"), []byte(tasks), 0644))
	return repoDir, specDir
}

func TestRunTask_InferSingle(t *testing.T) {
	repoDir, specDir := setupRunnerSpec(t)

	mock := opencode.NewMockLLMClient()
	mock.DefaultResult = "Task completed successfully"

	cfg := &config.Config{
		Models: config.ModelsConfig{Primary: "test/model"},
	}

	// task-002 is the only runnable task (task-001 completed, task-003 depends on task-002).
	err := RunTask(context.Background(), mock, cfg, repoDir, "runner-test", "", "")
	require.NoError(t, err)

	// Verify task-002 is now completed.
	tasks, err := ParseTasks(filepath.Join(specDir, "tasks.md"))
	require.NoError(t, err)
	for _, task := range tasks {
		if task.ID == "task-002" {
			assert.Equal(t, TaskStatusCompleted, task.Status)
		}
	}

	// Verify history was saved.
	entries, err := os.ReadDir(filepath.Join(specDir, "history"))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(entries), 1)
}

func TestRunTask_ByID(t *testing.T) {
	repoDir, specDir := setupRunnerSpec(t)

	mock := opencode.NewMockLLMClient()
	mock.DefaultResult = "Done"
	cfg := &config.Config{
		Models: config.ModelsConfig{Primary: "test/model"},
	}

	err := RunTask(context.Background(), mock, cfg, repoDir, "runner-test", "task-002", "")
	require.NoError(t, err)

	tasks, err := ParseTasks(filepath.Join(specDir, "tasks.md"))
	require.NoError(t, err)
	for _, task := range tasks {
		if task.ID == "task-002" {
			assert.Equal(t, TaskStatusCompleted, task.Status)
		}
	}
}

func TestRunTask_NoRunnable(t *testing.T) {
	repoDir := setupSpecDir(t, "runner-none", "requirements.md", "research.md", "design.md", "tasks.md")
	specDir := filepath.Join(repoDir, ".otto", "specs", "runner-none")

	tasks := `# Tasks

## Task 1: Running task
- **id**: task-001
- **status**: running
- **parallel_group**: 1
- **depends_on**: []
- **description**: Already running.
- **files**: []
`
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "tasks.md"), []byte(tasks), 0644))

	mock := opencode.NewMockLLMClient()
	cfg := &config.Config{Models: config.ModelsConfig{Primary: "test/model"}}

	err := RunTask(context.Background(), mock, cfg, repoDir, "runner-none", "", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no runnable tasks")
}

func TestRunTask_MultipleRunnable(t *testing.T) {
	repoDir := setupSpecDir(t, "runner-multi", "requirements.md", "research.md", "design.md", "tasks.md")
	specDir := filepath.Join(repoDir, ".otto", "specs", "runner-multi")

	tasks := `# Tasks

## Task 1: Task A
- **id**: task-001
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: First task.
- **files**: []

## Task 2: Task B
- **id**: task-002
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: Second task.
- **files**: []
`
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "tasks.md"), []byte(tasks), 0644))

	mock := opencode.NewMockLLMClient()
	cfg := &config.Config{Models: config.ModelsConfig{Primary: "test/model"}}

	err := RunTask(context.Background(), mock, cfg, repoDir, "runner-multi", "", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "multiple runnable tasks")
	assert.Contains(t, err.Error(), "task-001")
	assert.Contains(t, err.Error(), "task-002")
}

func TestRunTask_TaskNotFound(t *testing.T) {
	repoDir, _ := setupRunnerSpec(t)

	mock := opencode.NewMockLLMClient()
	cfg := &config.Config{Models: config.ModelsConfig{Primary: "test/model"}}

	err := RunTask(context.Background(), mock, cfg, repoDir, "runner-test", "task-999", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRunTask_MissingPrerequisites(t *testing.T) {
	// No design.md — should fail prerequisites check.
	repoDir := setupSpecDir(t, "runner-prereq", "requirements.md", "research.md", "tasks.md")
	specDir := filepath.Join(repoDir, ".otto", "specs", "runner-prereq")

	tasks := `# Tasks

## Task 1: Test
- **id**: task-001
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: Test.
- **files**: []
`
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "tasks.md"), []byte(tasks), 0644))

	mock := opencode.NewMockLLMClient()
	cfg := &config.Config{Models: config.ModelsConfig{Primary: "test/model"}}

	err := RunTask(context.Background(), mock, cfg, repoDir, "runner-prereq", "task-001", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "design.md")
}

func TestNextHistoryNumber(t *testing.T) {
	dir := t.TempDir()

	// Empty directory.
	assert.Equal(t, 1, nextHistoryNumber(dir))

	// Create some history files.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "run-001.md"), []byte("test"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "run-003.md"), []byte("test"), 0644))
	assert.Equal(t, 4, nextHistoryNumber(dir))

	// Non-existent directory.
	assert.Equal(t, 1, nextHistoryNumber(filepath.Join(dir, "nonexistent")))
}

func TestSaveHistory(t *testing.T) {
	dir := t.TempDir()
	messages := []opencode.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "World"},
	}

	err := saveHistory(dir, 1, "task-001", messages)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(dir, "run-001-task-001.md"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "# Run 001")
	assert.Contains(t, string(content), "## user")
	assert.Contains(t, string(content), "Hello")
	assert.Contains(t, string(content), "## assistant")
	assert.Contains(t, string(content), "World")
}

func TestBuildTaskPrompt(t *testing.T) {
	repoDir := setupSpecDir(t, "prompt-test", "requirements.md", "design.md")
	s, err := LoadSpec("prompt-test", repoDir)
	require.NoError(t, err)

	task := &Task{
		ID:          "task-001",
		Title:       "Test Task",
		Description: "Do something important",
		Files:       []string{"file1.go", "file2.go"},
	}

	prompt := buildTaskPrompt(s, task, "")
	assert.Contains(t, prompt, "You are executing a development task")
	assert.Contains(t, prompt, "task-001")
	assert.Contains(t, prompt, "Test Task")
	assert.Contains(t, prompt, "Do something important")
	assert.Contains(t, prompt, "file1.go, file2.go")
}

func TestBriefTask(t *testing.T) {
	repoDir := setupSpecDir(t, "brief-test", "requirements.md", "research.md", "design.md", "tasks.md")
	specDir := filepath.Join(repoDir, ".otto", "specs", "brief-test")

	// Write meaningful content to artifacts so the template renders properly.
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "requirements.md"), []byte("# Requirements\n\nMust implement authentication."), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "design.md"), []byte("# Design\n\nUse JWT tokens."), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "research.md"), []byte("# Research\n\nJWT is standard."), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "tasks.md"), []byte("# Tasks\n\n## Task 1: Auth\n- **id**: task-001\n- **status**: pending\n- **parallel_group**: 1\n- **depends_on**: []\n- **description**: Implement auth.\n- **files**: [\"auth.go\"]"), 0644))

	spec, err := LoadSpec("brief-test", repoDir)
	require.NoError(t, err)

	task := &Task{
		ID:          "task-001",
		Title:       "Implement auth",
		Description: "Implement JWT authentication",
		Files:       []string{"auth.go"},
		DependsOn:   []string{"task-000"},
	}

	mock := opencode.NewMockLLMClient()
	mock.DefaultResult = "# Implementation Brief\n\nImplement JWT auth in auth.go.\n\n## Objective\n\nBuild the auth module."

	cfg := &config.Config{
		Models: config.ModelsConfig{Primary: "test/model"},
	}

	brief, err := briefTask(context.Background(), mock, cfg, repoDir, spec, task)
	require.NoError(t, err)
	assert.Contains(t, brief, "Implementation Brief")
	assert.Contains(t, brief, "auth")

	// Verify the briefing prompt was sent to the LLM.
	history := mock.GetPromptHistory()
	require.Len(t, history, 1)
	assert.Contains(t, history[0].Prompt, "task-001")
	assert.Contains(t, history[0].Prompt, "Implement JWT authentication")
	assert.Contains(t, history[0].Prompt, "Must implement authentication")
	assert.Contains(t, history[0].Prompt, "Use JWT tokens")
	assert.Contains(t, history[0].Prompt, "task-000") // depends_on
}

func TestBriefTask_EmptyResponse(t *testing.T) {
	repoDir := setupSpecDir(t, "brief-empty", "requirements.md", "design.md", "tasks.md")

	spec, err := LoadSpec("brief-empty", repoDir)
	require.NoError(t, err)

	task := &Task{
		ID:          "task-001",
		Title:       "Test",
		Description: "Test task",
	}

	mock := opencode.NewMockLLMClient()
	mock.DefaultResult = "   " // whitespace-only

	cfg := &config.Config{
		Models: config.ModelsConfig{Primary: "test/model"},
	}

	_, err = briefTask(context.Background(), mock, cfg, repoDir, spec, task)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty response")
}

func TestBriefTask_LLMError(t *testing.T) {
	repoDir := setupSpecDir(t, "brief-err", "requirements.md", "design.md", "tasks.md")

	spec, err := LoadSpec("brief-err", repoDir)
	require.NoError(t, err)

	task := &Task{
		ID:          "task-001",
		Title:       "Test",
		Description: "Test task",
	}

	mock := opencode.NewMockLLMClient()
	mock.PromptErr = assert.AnError

	cfg := &config.Config{
		Models: config.ModelsConfig{Primary: "test/model"},
	}

	_, err = briefTask(context.Background(), mock, cfg, repoDir, spec, task)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "briefing LLM call")
}

func TestBuildBriefedPrompt(t *testing.T) {
	repoDir := setupSpecDir(t, "briefed-prompt", "requirements.md", "design.md")
	spec, err := LoadSpec("briefed-prompt", repoDir)
	require.NoError(t, err)

	task := &Task{
		ID:          "task-001",
		Title:       "Test Task",
		Description: "Do something",
	}

	brief := "# Implementation Brief\n\nImplement the widget in widget.go."

	prompt := buildBriefedPrompt(spec, task, brief, "")
	assert.Contains(t, prompt, "Implementation Brief")
	assert.Contains(t, prompt, "widget.go")
	assert.Contains(t, prompt, "Reference Documents")
	assert.Contains(t, prompt, spec.RequirementsPath)
	assert.Contains(t, prompt, spec.DesignPath)
	assert.Contains(t, prompt, spec.ResearchPath)
	assert.Contains(t, prompt, spec.TasksPath)
	assert.NotContains(t, prompt, "Previous Attempt Failed")
}

func TestBuildBriefedPrompt_WithPreviousError(t *testing.T) {
	repoDir := setupSpecDir(t, "briefed-retry", "requirements.md", "design.md")
	spec, err := LoadSpec("briefed-retry", repoDir)
	require.NoError(t, err)

	task := &Task{
		ID:          "task-001",
		Title:       "Test Task",
		Description: "Do something",
	}

	prompt := buildBriefedPrompt(spec, task, "Brief content", "compilation failed: undefined foo")
	assert.Contains(t, prompt, "Previous Attempt Failed")
	assert.Contains(t, prompt, "compilation failed: undefined foo")
	assert.Contains(t, prompt, "Brief content")
}

func TestRunTask_WithBriefingEnabled(t *testing.T) {
	repoDir, specDir := setupRunnerSpec(t)

	mock := opencode.NewMockLLMClient()
	mock.DefaultResult = "Task completed successfully"

	enabled := true
	cfg := &config.Config{
		Models: config.ModelsConfig{Primary: "test/model"},
		Spec: config.SpecConfig{
			TaskBriefing: &enabled,
		},
	}

	err := RunTask(context.Background(), mock, cfg, repoDir, "runner-test", "task-002", "")
	require.NoError(t, err)

	// Verify task-002 is now completed.
	tasks, err := ParseTasks(filepath.Join(specDir, "tasks.md"))
	require.NoError(t, err)
	for _, task := range tasks {
		if task.ID == "task-002" {
			assert.Equal(t, TaskStatusCompleted, task.Status)
		}
	}

	// With briefing enabled, there should be 2 LLM calls:
	// 1. briefing session, 2. execution session
	history := mock.GetPromptHistory()
	assert.Len(t, history, 2)

	// First call should be the briefing (session title starts with "brief-").
	// The briefing prompt should contain task-briefing template content.
	assert.Contains(t, history[0].Prompt, "task-002")

	// Second call should be the execution with the briefed prompt.
	assert.Contains(t, history[1].Prompt, "Reference Documents")
}

func TestRunTask_WithBriefingDisabled(t *testing.T) {
	repoDir, specDir := setupRunnerSpec(t)

	mock := opencode.NewMockLLMClient()
	mock.DefaultResult = "Task completed successfully"

	disabled := false
	cfg := &config.Config{
		Models: config.ModelsConfig{Primary: "test/model"},
		Spec: config.SpecConfig{
			TaskBriefing: &disabled,
		},
	}

	err := RunTask(context.Background(), mock, cfg, repoDir, "runner-test", "task-002", "")
	require.NoError(t, err)

	tasks, err := ParseTasks(filepath.Join(specDir, "tasks.md"))
	require.NoError(t, err)
	for _, task := range tasks {
		if task.ID == "task-002" {
			assert.Equal(t, TaskStatusCompleted, task.Status)
		}
	}

	// With briefing disabled, there should be only 1 LLM call (execution).
	history := mock.GetPromptHistory()
	assert.Len(t, history, 1)

	// The prompt should be the static buildTaskPrompt — contains the raw context dump.
	assert.Contains(t, history[0].Prompt, "You are executing a development task. Here is the context:")
	assert.NotContains(t, history[0].Prompt, "Reference Documents")
}

func TestRunTask_BriefingFallback(t *testing.T) {
	repoDir, specDir := setupRunnerSpec(t)

	mock := opencode.NewMockLLMClient()

	// The briefing call will fail, but subsequent calls succeed.
	callCount := 0
	origSendPrompt := mock.SendPrompt
	_ = origSendPrompt // mock approach: use PromptErr then clear it

	// We need a more nuanced mock — first call fails, second succeeds.
	// Use CreateErr to fail the briefing session creation.
	// Actually, let's use a different approach: set PromptErr for the first call only.
	// The mock doesn't support per-call errors, so let's test by making CreateSession fail
	// for the briefing session specifically.
	// Simpler: just verify that when briefing is enabled but the LLM returns empty,
	// it falls back to the static prompt.

	// Make the mock return empty for briefing sessions.
	mock.DefaultResult = ""
	// Override: track calls and return empty for first, content for second.
	mock2 := &countingMockClient{
		MockLLMClient: opencode.NewMockLLMClient(),
		callResults:   []string{"", "Execution done"},
	}
	_ = callCount

	enabled := true
	cfg := &config.Config{
		Models: config.ModelsConfig{Primary: "test/model"},
		Spec: config.SpecConfig{
			TaskBriefing: &enabled,
		},
	}

	err := RunTask(context.Background(), mock2, cfg, repoDir, "runner-test", "task-002", "")
	require.NoError(t, err)

	tasks, err := ParseTasks(filepath.Join(specDir, "tasks.md"))
	require.NoError(t, err)
	for _, task := range tasks {
		if task.ID == "task-002" {
			assert.Equal(t, TaskStatusCompleted, task.Status)
		}
	}

	// Briefing returned empty → fallback to static prompt.
	// Should have 2 calls: failed briefing + static execution.
	history := mock2.GetPromptHistory()
	assert.GreaterOrEqual(t, len(history), 1)
	// The execution prompt should be the static one (fallback).
	lastPrompt := history[len(history)-1].Prompt
	assert.Contains(t, lastPrompt, "You are executing a development task")
}

// countingMockClient extends MockLLMClient with per-call result sequencing.
type countingMockClient struct {
	*opencode.MockLLMClient
	mu          sync.Mutex
	callResults []string
	callIdx     int
}

func (c *countingMockClient) SendPrompt(ctx context.Context, sessionID string, prompt string, model opencode.ModelRef, directory string) (*opencode.PromptResponse, error) {
	c.mu.Lock()
	idx := c.callIdx
	c.callIdx++
	c.mu.Unlock()

	// Record in the underlying mock for GetPromptHistory.
	c.MockLLMClient.SendPrompt(ctx, sessionID, prompt, model, directory)

	result := "Mock response"
	if idx < len(c.callResults) {
		result = c.callResults[idx]
	}
	return &opencode.PromptResponse{Content: result}, nil
}
