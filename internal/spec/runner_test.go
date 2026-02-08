package spec

import (
	"context"
	"os"
	"path/filepath"
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
	err := RunTask(context.Background(), mock, cfg, repoDir, "runner-test", "")
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

	err := RunTask(context.Background(), mock, cfg, repoDir, "runner-test", "task-002")
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

	err := RunTask(context.Background(), mock, cfg, repoDir, "runner-none", "")
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

	err := RunTask(context.Background(), mock, cfg, repoDir, "runner-multi", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "multiple runnable tasks")
	assert.Contains(t, err.Error(), "task-001")
	assert.Contains(t, err.Error(), "task-002")
}

func TestRunTask_TaskNotFound(t *testing.T) {
	repoDir, _ := setupRunnerSpec(t)

	mock := opencode.NewMockLLMClient()
	cfg := &config.Config{Models: config.ModelsConfig{Primary: "test/model"}}

	err := RunTask(context.Background(), mock, cfg, repoDir, "runner-test", "task-999")
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

	err := RunTask(context.Background(), mock, cfg, repoDir, "runner-prereq", "task-001")
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

	err := saveHistory(dir, 1, messages)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(dir, "run-001.md"))
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

	prompt := buildTaskPrompt(s, task)
	assert.Contains(t, prompt, "You are executing a development task")
	assert.Contains(t, prompt, "task-001")
	assert.Contains(t, prompt, "Test Task")
	assert.Contains(t, prompt, "Do something important")
	assert.Contains(t, prompt, "file1.go, file2.go")
}
