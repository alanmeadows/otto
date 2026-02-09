package spec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/opencode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Test helpers ---

// setupExecuteRepo creates a temp dir with a git repo and full spec prerequisites.
func setupExecuteRepo(t *testing.T, slug string, tasksMD string) string {
	t.Helper()

	repoDir := t.TempDir()

	// Initialize git repo (handles WSL safe.directory).
	initGitRepo(t, repoDir)

	// Set up spec directory with all prerequisites.
	specDir := filepath.Join(repoDir, ".otto", "specs", slug)
	require.NoError(t, os.MkdirAll(filepath.Join(specDir, "history"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "requirements.md"), []byte("# Requirements"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "research.md"), []byte("# Research"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "design.md"), []byte("# Design"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "tasks.md"), []byte(tasksMD), 0644))

	// Commit spec files so they're tracked.
	execGit(t, repoDir, "add", "-A")
	execGit(t, repoDir, "commit", "-m", "add spec")

	return repoDir
}

// initGitRepo initializes a git repo in dir with an initial commit.
// Handles WSL / VFS for Git safe.directory issues.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()

	// Mark directory as safe (needed for WSL / temp dirs with different ownership).
	safeDirCmd := exec.Command("git", "config", "--global", "--add", "safe.directory", dir)
	_ = safeDirCmd.Run() // best effort

	commands := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "commit", "--allow-empty", "-m", "initial"},
	}
	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "command %v failed: %s", args, string(out))
	}
}

// execGit runs a git command in the given directory.
func execGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), string(out))
}

// defaultTestConfig returns a minimal config for testing.
func defaultTestConfig() *config.Config {
	return &config.Config{
		Models: config.ModelsConfig{
			Primary:   "test/primary",
			Secondary: "test/secondary",
		},
		Spec: config.SpecConfig{
			MaxParallelTasks: 2,
			TaskTimeout:      "5m",
			MaxTaskRetries:   0,
		},
	}
}

// failNTimesClient wraps MockLLMClient and fails the first N SendPrompt calls.
type failNTimesClient struct {
	*opencode.MockLLMClient
	failMu    sync.Mutex
	callCount int
	failUntil int
}

func (f *failNTimesClient) SendPrompt(ctx context.Context, sessionID, prompt string, model opencode.ModelRef, directory string) (*opencode.PromptResponse, error) {
	f.failMu.Lock()
	f.callCount++
	count := f.callCount
	f.failMu.Unlock()

	if count <= f.failUntil {
		return nil, fmt.Errorf("simulated failure %d", count)
	}
	return f.MockLLMClient.SendPrompt(ctx, sessionID, prompt, model, directory)
}

// --- Unit tests for helper functions ---

func TestRecoverCrashedTasks(t *testing.T) {
	repoDir := setupSpecDir(t, "recover-test", "requirements.md", "research.md", "design.md", "tasks.md")
	specDir := filepath.Join(repoDir, ".otto", "specs", "recover-test")

	tasksMD := `# Tasks

## Task 1: Crashed
- **id**: task-001
- **status**: running
- **parallel_group**: 1
- **depends_on**: []
- **description**: Was running.
- **files**: []

## Task 2: Normal
- **id**: task-002
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: Is pending.
- **files**: []
`
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "tasks.md"), []byte(tasksMD), 0644))

	spec, err := LoadSpec("recover-test", repoDir)
	require.NoError(t, err)

	tasks, err := ParseTasks(spec.TasksPath)
	require.NoError(t, err)

	err = recoverCrashedTasks(spec, tasks)
	require.NoError(t, err)

	// Re-parse and verify.
	tasks, err = ParseTasks(spec.TasksPath)
	require.NoError(t, err)
	assert.Equal(t, TaskStatusPending, tasks[0].Status)
	assert.Equal(t, TaskStatusPending, tasks[1].Status)
}

func TestPhaseAllCompleted(t *testing.T) {
	completed := []Task{
		{ID: "t-1", Status: TaskStatusCompleted},
		{ID: "t-2", Status: TaskStatusCompleted},
	}
	assert.True(t, phaseAllCompleted(completed))

	mixed := []Task{
		{ID: "t-1", Status: TaskStatusCompleted},
		{ID: "t-2", Status: TaskStatusPending},
	}
	assert.False(t, phaseAllCompleted(mixed))
}

func TestResultsAllFailed(t *testing.T) {
	allFail := []taskResult{
		{TaskID: "t-1", Err: fmt.Errorf("fail")},
		{TaskID: "t-2", Err: fmt.Errorf("fail")},
	}
	assert.True(t, resultsAllFailed(allFail))

	mixed := []taskResult{
		{TaskID: "t-1", Err: nil},
		{TaskID: "t-2", Err: fmt.Errorf("fail")},
	}
	assert.False(t, resultsAllFailed(mixed))

	empty := []taskResult{}
	assert.True(t, resultsAllFailed(empty))

	allOk := []taskResult{
		{TaskID: "t-1", Err: nil},
	}
	assert.False(t, resultsAllFailed(allOk))
}

func TestCheckFileOverlaps(t *testing.T) {
	// With overlaps — should not panic.
	checkFileOverlaps([]Task{
		{ID: "t-1", Files: []string{"a.go", "shared.go"}},
		{ID: "t-2", Files: []string{"b.go", "shared.go"}},
	})

	// Without overlaps — should not panic.
	checkFileOverlaps([]Task{
		{ID: "t-1", Files: []string{"a.go"}},
		{ID: "t-2", Files: []string{"b.go"}},
	})

	// Empty files — should not panic.
	checkFileOverlaps([]Task{
		{ID: "t-1", Files: []string{}},
	})

	// No tasks — should not panic.
	checkFileOverlaps([]Task{})
}

func TestCollectPhaseLogs(t *testing.T) {
	dir := t.TempDir()

	// Create history files.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "run-001.md"), []byte("log 1"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "run-002.md"), []byte("log 2"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "run-003.md"), []byte("log 3"), 0644))

	// Baseline 1: should get only logs after 1.
	logs := collectPhaseLogs(dir, 1)
	assert.Contains(t, logs, "log 2")
	assert.Contains(t, logs, "log 3")
	assert.NotContains(t, logs, "log 1")

	// Baseline 3: should get nothing.
	logs = collectPhaseLogs(dir, 3)
	assert.Empty(t, logs)

	// Baseline 0: should get everything.
	logs = collectPhaseLogs(dir, 0)
	assert.Contains(t, logs, "log 1")
	assert.Contains(t, logs, "log 2")
	assert.Contains(t, logs, "log 3")

	// Non-existent dir: should return empty.
	logs = collectPhaseLogs(filepath.Join(dir, "nonexistent"), 0)
	assert.Empty(t, logs)
}

func TestGitHelpers(t *testing.T) {
	dir := t.TempDir()

	initGitRepo(t, dir)

	// Create and commit a tracked file.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0644))
	execGit(t, dir, "add", "-A")
	execGit(t, dir, "commit", "-m", "add file")

	// No changes.
	assert.False(t, gitHasChanges(dir))

	// Modify tracked file.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello world"), 0644))
	assert.True(t, gitHasChanges(dir))

	// Git diff shows the change.
	diff, err := gitDiff(dir)
	require.NoError(t, err)
	assert.Contains(t, diff, "hello world")

	// Commit.
	err = gitCommit(dir, "test commit")
	require.NoError(t, err)
	assert.False(t, gitHasChanges(dir))
}

func TestCommitPhase(t *testing.T) {
	dir := t.TempDir()

	initGitRepo(t, dir)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "init.txt"), []byte("init"), 0644))
	execGit(t, dir, "add", "-A")
	execGit(t, dir, "commit", "-m", "add init")

	// Create a change.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "change.txt"), []byte("changed"), 0644))

	phase := []Task{{ID: "task-1", Title: "Test Task"}}
	committed := commitPhase(dir, 1, phase)
	assert.True(t, committed)

	// Verify commit message.
	cmd := exec.Command("git", "log", "--oneline", "-1")
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "otto: phase 1")
	assert.Contains(t, string(out), "Test Task")
}

func TestCommitPhase_NoChanges(t *testing.T) {
	dir := t.TempDir()

	initGitRepo(t, dir)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "init.txt"), []byte("init"), 0644))
	execGit(t, dir, "add", "-A")
	execGit(t, dir, "commit", "-m", "add init")

	// No new changes.
	phase := []Task{{ID: "task-1", Title: "Test Task"}}
	committed := commitPhase(dir, 1, phase)
	assert.False(t, committed)
}

// --- Integration tests ---

func TestExecute_EmptyTasks(t *testing.T) {
	tasksMD := "# Tasks\n\nNo tasks defined."
	repoDir := setupExecuteRepo(t, "empty-tasks", tasksMD)

	mock := opencode.NewMockLLMClient()
	cfg := defaultTestConfig()

	err := Execute(context.Background(), mock, cfg, repoDir, "empty-tasks")
	require.NoError(t, err)

	// No sessions should have been created.
	assert.Empty(t, mock.GetPromptHistory())
}

func TestExecute_AllCompleted(t *testing.T) {
	tasksMD := `# Tasks

## Task 1: Done task
- **id**: task-001
- **status**: completed
- **parallel_group**: 1
- **depends_on**: []
- **description**: Already done.
- **files**: []

## Task 2: Also done
- **id**: task-002
- **status**: completed
- **parallel_group**: 2
- **depends_on**: [task-001]
- **description**: Already done too.
- **files**: []
`
	repoDir := setupExecuteRepo(t, "all-done", tasksMD)

	mock := opencode.NewMockLLMClient()
	cfg := defaultTestConfig()

	err := Execute(context.Background(), mock, cfg, repoDir, "all-done")
	require.NoError(t, err)

	// No prompts sent since all phases were skipped.
	assert.Empty(t, mock.GetPromptHistory())
}

func TestExecute_SinglePhase(t *testing.T) {
	tasksMD := `# Tasks

## Task 1: First task
- **id**: task-001
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: Do something.
- **files**: ["file.go"]
`
	repoDir := setupExecuteRepo(t, "single-phase", tasksMD)

	mock := opencode.NewMockLLMClient()
	cfg := defaultTestConfig()

	err := Execute(context.Background(), mock, cfg, repoDir, "single-phase")
	require.NoError(t, err)

	// Verify task completed.
	spec, _ := LoadSpec("single-phase", repoDir)
	tasks, err := ParseTasks(spec.TasksPath)
	require.NoError(t, err)
	assert.Equal(t, TaskStatusCompleted, tasks[0].Status)

	// Verify at least one prompt was sent for the task execution.
	assert.GreaterOrEqual(t, len(mock.GetPromptHistory()), 1)
}

func TestExecute_MultiPhase(t *testing.T) {
	tasksMD := `# Tasks

## Task 1: Phase one
- **id**: task-001
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: First phase.
- **files**: []

## Task 2: Phase two
- **id**: task-002
- **status**: pending
- **parallel_group**: 2
- **depends_on**: [task-001]
- **description**: Second phase.
- **files**: []
`
	repoDir := setupExecuteRepo(t, "multi-phase", tasksMD)

	mock := opencode.NewMockLLMClient()
	cfg := defaultTestConfig()

	err := Execute(context.Background(), mock, cfg, repoDir, "multi-phase")
	require.NoError(t, err)

	// Both tasks completed.
	spec, _ := LoadSpec("multi-phase", repoDir)
	tasks, err := ParseTasks(spec.TasksPath)
	require.NoError(t, err)
	for _, task := range tasks {
		assert.Equal(t, TaskStatusCompleted, task.Status, "task %s should be completed", task.ID)
	}

	// Multiple prompts sent (task executions + review + summary + harvest per phase).
	assert.GreaterOrEqual(t, len(mock.GetPromptHistory()), 2)
}

func TestExecute_SkipsCompletedPhases(t *testing.T) {
	tasksMD := `# Tasks

## Task 1: Already done
- **id**: task-001
- **status**: completed
- **parallel_group**: 1
- **depends_on**: []
- **description**: First phase already done.
- **files**: []

## Task 2: Still pending
- **id**: task-002
- **status**: pending
- **parallel_group**: 2
- **depends_on**: [task-001]
- **description**: Second phase pending.
- **files**: []
`
	repoDir := setupExecuteRepo(t, "skip-phase", tasksMD)

	mock := opencode.NewMockLLMClient()
	cfg := defaultTestConfig()

	err := Execute(context.Background(), mock, cfg, repoDir, "skip-phase")
	require.NoError(t, err)

	// Phase 1 was skipped, phase 2 was executed.
	spec, _ := LoadSpec("skip-phase", repoDir)
	tasks, err := ParseTasks(spec.TasksPath)
	require.NoError(t, err)
	assert.Equal(t, TaskStatusCompleted, tasks[0].Status)
	assert.Equal(t, TaskStatusCompleted, tasks[1].Status)
}

func TestExecute_CrashRecovery(t *testing.T) {
	tasksMD := `# Tasks

## Task 1: Crashed task
- **id**: task-001
- **status**: running
- **parallel_group**: 1
- **depends_on**: []
- **description**: Was running when crash occurred.
- **files**: []
`
	repoDir := setupExecuteRepo(t, "crash-recovery", tasksMD)

	mock := opencode.NewMockLLMClient()
	cfg := defaultTestConfig()

	err := Execute(context.Background(), mock, cfg, repoDir, "crash-recovery")
	require.NoError(t, err)

	// Verify task was completed (crash recovery reset to pending, then ran).
	spec, _ := LoadSpec("crash-recovery", repoDir)
	tasks, err := ParseTasks(spec.TasksPath)
	require.NoError(t, err)
	assert.Equal(t, TaskStatusCompleted, tasks[0].Status)
}

func TestExecute_RetryLogic(t *testing.T) {
	tasksMD := `# Tasks

## Task 1: Flaky task
- **id**: task-001
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: This task will fail once then succeed.
- **files**: []
`
	repoDir := setupExecuteRepo(t, "retry-test", tasksMD)

	baseMock := opencode.NewMockLLMClient()
	mock := &failNTimesClient{
		MockLLMClient: baseMock,
		failUntil:     1, // First SendPrompt call fails, rest succeed.
	}

	cfg := defaultTestConfig()
	cfg.Spec.MaxTaskRetries = 2

	err := Execute(context.Background(), mock, cfg, repoDir, "retry-test")
	require.NoError(t, err)

	// Task should be completed after retry.
	spec, _ := LoadSpec("retry-test", repoDir)
	tasks, err := ParseTasks(spec.TasksPath)
	require.NoError(t, err)
	assert.Equal(t, TaskStatusCompleted, tasks[0].Status)
}

func TestExecute_ContextCancellation(t *testing.T) {
	tasksMD := `# Tasks

## Task 1: Task one
- **id**: task-001
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: First task.
- **files**: []
`
	repoDir := setupExecuteRepo(t, "cancel-test", tasksMD)

	mock := opencode.NewMockLLMClient()
	cfg := defaultTestConfig()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	err := Execute(ctx, mock, cfg, repoDir, "cancel-test")
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestExecute_FileOverlap(t *testing.T) {
	tasksMD := `# Tasks

## Task 1: Overlapping A
- **id**: task-001
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: Touches shared file.
- **files**: ["shared.go", "a.go"]

## Task 2: Overlapping B
- **id**: task-002
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: Also touches shared file.
- **files**: ["shared.go", "b.go"]
`
	repoDir := setupExecuteRepo(t, "overlap-test", tasksMD)

	mock := opencode.NewMockLLMClient()
	cfg := defaultTestConfig()

	// Should not crash despite overlapping files.
	err := Execute(context.Background(), mock, cfg, repoDir, "overlap-test")
	require.NoError(t, err)

	// Both tasks should still complete.
	spec, _ := LoadSpec("overlap-test", repoDir)
	tasks, err := ParseTasks(spec.TasksPath)
	require.NoError(t, err)
	for _, task := range tasks {
		assert.Equal(t, TaskStatusCompleted, task.Status, "task %s should be completed", task.ID)
	}
}

func TestExecute_PhaseCommit(t *testing.T) {
	tasksMD := `# Tasks

## Task 1: Commit test
- **id**: task-001
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: Test commit.
- **files**: []
`
	repoDir := setupExecuteRepo(t, "commit-test", tasksMD)

	mock := opencode.NewMockLLMClient()
	cfg := defaultTestConfig()

	err := Execute(context.Background(), mock, cfg, repoDir, "commit-test")
	require.NoError(t, err)

	// Verify a commit was made with "otto: phase" in the message.
	cmd := exec.Command("git", "log", "--oneline")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "otto: phase")
}

func TestExecute_MissingPrerequisites(t *testing.T) {
	// Missing design.md → should fail prerequisites check.
	repoDir := setupSpecDir(t, "exec-prereq", "requirements.md", "research.md", "tasks.md")
	specDir := filepath.Join(repoDir, ".otto", "specs", "exec-prereq")

	tasksMD := `# Tasks

## Task 1: Test
- **id**: task-001
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: Test.
- **files**: []
`
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "tasks.md"), []byte(tasksMD), 0644))

	mock := opencode.NewMockLLMClient()
	cfg := defaultTestConfig()

	err := Execute(context.Background(), mock, cfg, repoDir, "exec-prereq")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "design.md")
}

func TestExecute_PhaseSummaryWritten(t *testing.T) {
	tasksMD := `# Tasks

## Task 1: Summary test
- **id**: task-001
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: Verify phase summary is written.
- **files**: []
`
	repoDir := setupExecuteRepo(t, "summary-test", tasksMD)

	mock := opencode.NewMockLLMClient()
	mock.DefaultResult = "Phase summary content"
	cfg := defaultTestConfig()

	err := Execute(context.Background(), mock, cfg, repoDir, "summary-test")
	require.NoError(t, err)

	// Verify that history/phase-1-summary.md exists.
	spec, err := LoadSpec("summary-test", repoDir)
	require.NoError(t, err)
	summaryPath := filepath.Join(spec.HistoryDir, "phase-1-summary.md")
	_, err = os.Stat(summaryPath)
	assert.NoError(t, err, "phase-1-summary.md should exist after execution")
}

func TestExecute_QuestionsHarvested(t *testing.T) {
	tasksMD := `# Tasks

## Task 1: Harvest test
- **id**: task-001
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: Verify questions are harvested.
- **files**: []
`
	repoDir := setupExecuteRepo(t, "harvest-test", tasksMD)

	mock := opencode.NewMockLLMClient()
	mock.DefaultResult = "Some harvested question content"
	cfg := defaultTestConfig()

	err := Execute(context.Background(), mock, cfg, repoDir, "harvest-test")
	require.NoError(t, err)

	// Verify that questions.md was created/appended to.
	spec, err := LoadSpec("harvest-test", repoDir)
	require.NoError(t, err)
	_, err = os.Stat(spec.QuestionsPath)
	assert.NoError(t, err, "questions.md should exist after execution with question harvesting")
}
