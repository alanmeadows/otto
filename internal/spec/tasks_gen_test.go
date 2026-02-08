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
