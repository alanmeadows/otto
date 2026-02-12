package spec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/opencode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- validateTaskOutput unit tests ---

func TestValidateTaskOutput_Valid(t *testing.T) {
	content := `# Tasks

## Task 1: Setup project
- **id**: task-001
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: Create the project.
- **files**: ["main.go"]
`
	tasks, err := validateTaskOutput(content)
	require.NoError(t, err)
	assert.Len(t, tasks, 1)
	assert.Equal(t, "task-001", tasks[0].ID)
}

func TestValidateTaskOutput_ZeroTasks(t *testing.T) {
	// Raw CONTRIBUTING.md content — no structured tasks.
	content := `# Contributing

Thank you for your interest in contributing!

## Getting Started

1. Fork the repository
2. Create a feature branch
3. Submit a pull request
`
	tasks, err := validateTaskOutput(content)
	assert.Error(t, err)
	assert.Nil(t, tasks)
	assert.Contains(t, err.Error(), "no structured tasks found")
}

func TestValidateTaskOutput_EmptyContent(t *testing.T) {
	tasks, err := validateTaskOutput("")
	assert.Error(t, err)
	assert.Nil(t, tasks)
	assert.Contains(t, err.Error(), "no structured tasks found")
}

func TestValidateTaskOutput_ParseError(t *testing.T) {
	// Has task header but invalid status.
	content := `# Tasks

## Task 1: Bad status
- **id**: task-001
- **status**: bananas
- **parallel_group**: 1
- **depends_on**: []
- **description**: Test.
- **files**: []
`
	tasks, err := validateTaskOutput(content)
	assert.Error(t, err)
	assert.Nil(t, tasks)
	assert.Contains(t, err.Error(), "invalid task format")
}

// --- sequentialResultClient returns different results on successive SendPrompt calls ---

type sequentialResultClient struct {
	*opencode.MockLLMClient
	mu      sync.Mutex
	results []string
	callIdx int
}

func (s *sequentialResultClient) SendPrompt(ctx context.Context, sessionID, prompt string, model opencode.ModelRef, directory string) (*opencode.PromptResponse, error) {
	s.mu.Lock()
	idx := s.callIdx
	s.callIdx++
	s.mu.Unlock()

	var content string
	if idx < len(s.results) {
		content = s.results[idx]
	} else {
		content = s.results[len(s.results)-1] // repeat last result
	}

	// Still record in the mock for history tracking.
	s.MockLLMClient.SendPrompt(ctx, sessionID, prompt, model, directory)
	return &opencode.PromptResponse{Content: content}, nil
}

// --- SpecTaskAdd tests ---

func TestSpecTaskAdd(t *testing.T) {
	repoDir := setupSpecDir(t, "test-add", "requirements.md", "research.md", "design.md", "tasks.md")
	specDir := filepath.Join(repoDir, ".otto", "specs", "test-add")

	// Write existing tasks.
	existingTasks := `# Tasks

## Task 1: Setup project
- **id**: task-001
- **status**: completed
- **parallel_group**: 1
- **depends_on**: []
- **description**: Create initial project layout.
- **files**: ["cmd/main.go"]
`
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "tasks.md"), []byte(existingTasks), 0644))

	// Mock that returns a valid updated tasks.md.
	mock := opencode.NewMockLLMClient()
	mock.DefaultResult = `# Tasks

## Task 1: Setup project
- **id**: task-001
- **status**: completed
- **parallel_group**: 1
- **depends_on**: []
- **description**: Create initial project layout.
- **files**: ["cmd/main.go"]

## Task 2: Add logging
- **id**: task-002
- **status**: pending
- **parallel_group**: 2
- **depends_on**: [task-001]
- **description**: Add structured logging throughout the application.
- **files**: ["internal/logging/log.go"]
`

	cfg := &config.Config{
		Models: config.ModelsConfig{
			Primary: "anthropic/claude-sonnet-4-20250514",
		},
	}

	err := SpecTaskAdd(context.Background(), mock, cfg, repoDir, "test-add", "Add structured logging")
	require.NoError(t, err)

	// Verify tasks were written.
	tasks, err := ParseTasks(filepath.Join(specDir, "tasks.md"))
	require.NoError(t, err)
	assert.Len(t, tasks, 2)
	assert.Equal(t, "task-002", tasks[1].ID)

	// Verify a prompt was sent.
	history := mock.GetPromptHistory()
	require.Len(t, history, 1)
	assert.Contains(t, history[0].Prompt, "Add structured logging")
}

func TestSpecTaskAdd_NoExistingTasks(t *testing.T) {
	repoDir := setupSpecDir(t, "test-add-empty")
	specDir := filepath.Join(repoDir, ".otto", "specs", "test-add-empty")

	// No tasks.md exists — readArtifact returns empty.
	mock := opencode.NewMockLLMClient()
	mock.DefaultResult = `# Tasks

## Task 1: New task
- **id**: task-001
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: A brand new task.
- **files**: []
`

	cfg := &config.Config{
		Models: config.ModelsConfig{Primary: "test/model"},
	}

	err := SpecTaskAdd(context.Background(), mock, cfg, repoDir, "test-add-empty", "Create new task")
	require.NoError(t, err)

	tasks, err := ParseTasks(filepath.Join(specDir, "tasks.md"))
	require.NoError(t, err)
	assert.Len(t, tasks, 1)
}

// --- SpecTaskAdd retry tests ---

func TestSpecTaskAdd_RejectsNonTaskOutput(t *testing.T) {
	repoDir := setupSpecDir(t, "test-add-reject", "requirements.md", "research.md", "design.md", "tasks.md")

	// Mock always returns non-task content.
	mock := opencode.NewMockLLMClient()
	mock.DefaultResult = "# Contributing\n\nThis is a CONTRIBUTING.md file, not structured tasks."

	cfg := &config.Config{
		Models: config.ModelsConfig{Primary: "test/model"},
	}

	err := SpecTaskAdd(context.Background(), mock, cfg, repoDir, "test-add-reject", "Create CONTRIBUTING.md")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "task-add failed after")

	// tasks.md should NOT have been overwritten with the bad content.
	content, readErr := os.ReadFile(filepath.Join(repoDir, ".otto", "specs", "test-add-reject", "tasks.md"))
	require.NoError(t, readErr)
	assert.NotContains(t, string(content), "Contributing")
}

func TestSpecTaskAdd_RetriesAndSucceeds(t *testing.T) {
	repoDir := setupSpecDir(t, "test-add-retry", "requirements.md", "research.md", "design.md", "tasks.md")

	validTasks := `# Tasks

## Task 1: Create CONTRIBUTING.md
- **id**: task-001
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: Create a CONTRIBUTING.md with guidelines.
- **files**: ["CONTRIBUTING.md"]
`
	// First call returns bad content, second (retry) returns valid tasks.
	mock := &sequentialResultClient{
		MockLLMClient: opencode.NewMockLLMClient(),
		results: []string{
			"# Contributing\n\nThis is the actual file content, not tasks.",
			validTasks,
		},
	}

	cfg := &config.Config{
		Models: config.ModelsConfig{Primary: "test/model"},
	}

	err := SpecTaskAdd(context.Background(), mock, cfg, repoDir, "test-add-retry", "Create CONTRIBUTING.md")
	require.NoError(t, err)

	// Verify valid tasks were written.
	tasks, err := ParseTasks(filepath.Join(repoDir, ".otto", "specs", "test-add-retry", "tasks.md"))
	require.NoError(t, err)
	assert.Len(t, tasks, 1)
	assert.Equal(t, "Create CONTRIBUTING.md", tasks[0].Title)
}

// --- retryTaskGeneration unit test ---

func TestRetryTaskGeneration(t *testing.T) {
	mock := opencode.NewMockLLMClient()
	mock.DefaultResult = `# Tasks

## Task 1: Fixed task
- **id**: task-001
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: Now properly formatted.
- **files**: ["file.go"]
`

	model := opencode.ParseModelRef("test/model")
	dir := t.TempDir()
	invalidOutput := "# Contributing\n\nNot a task structure."
	validationErr := fmt.Errorf("no structured tasks found")

	result, err := retryTaskGeneration(context.Background(), mock, model, dir, invalidOutput, validationErr)
	require.NoError(t, err)
	assert.Contains(t, result, "## Task 1: Fixed task")

	// Verify the retry prompt mentioned the error.
	history := mock.GetPromptHistory()
	require.Len(t, history, 1)
	assert.Contains(t, history[0].Prompt, "no structured tasks found")
	assert.Contains(t, history[0].Prompt, "Not a task structure")
}
