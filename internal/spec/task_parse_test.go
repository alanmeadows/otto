package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleTasksMD = `# Tasks

## Task 1: Setup project structure
- **id**: task-001
- **status**: completed
- **parallel_group**: 1
- **depends_on**: []
- **description**: Create the initial project layout with cmd/, internal/, and pkg/ directories.
- **files**: ["cmd/main.go", "internal/app/app.go"]

## Task 2: Define core types
- **id**: task-002
- **status**: completed
- **parallel_group**: 1
- **depends_on**: []
- **description**: Define the core data types used throughout the application.
- **files**: ["internal/types/types.go", "internal/types/types_test.go"]

## Task 3: Implement HTTP server
- **id**: task-003
- **status**: pending
- **parallel_group**: 2
- **depends_on**: [task-001, task-002]
- **description**: Implement the main HTTP server with routing and middleware.
- **files**: ["internal/server/server.go", "internal/server/server_test.go"]

## Task 4: Implement auth middleware
- **id**: task-004
- **status**: pending
- **parallel_group**: 2
- **depends_on**: [task-002]
- **description**: Implement JWT authentication middleware.
- **files**: ["internal/auth/middleware.go", "internal/auth/middleware_test.go"]

## Task 5: Integration tests
- **id**: task-005
- **status**: pending
- **parallel_group**: 3
- **depends_on**: [task-003, task-004]
- **description**: Write integration tests for the full server with auth.
- **files**: ["internal/server/integration_test.go"]
`

func TestParseTasksFromString(t *testing.T) {
	tasks, err := ParseTasksFromString(sampleTasksMD)
	require.NoError(t, err)
	require.Len(t, tasks, 5)

	// Task 1
	assert.Equal(t, "task-001", tasks[0].ID)
	assert.Equal(t, "Setup project structure", tasks[0].Title)
	assert.Equal(t, TaskStatusCompleted, tasks[0].Status)
	assert.Equal(t, 1, tasks[0].ParallelGroup)
	assert.Empty(t, tasks[0].DependsOn)
	assert.Equal(t, []string{"cmd/main.go", "internal/app/app.go"}, tasks[0].Files)

	// Task 3
	assert.Equal(t, "task-003", tasks[2].ID)
	assert.Equal(t, TaskStatusPending, tasks[2].Status)
	assert.Equal(t, 2, tasks[2].ParallelGroup)
	assert.Equal(t, []string{"task-001", "task-002"}, tasks[2].DependsOn)

	// Task 5
	assert.Equal(t, "task-005", tasks[4].ID)
	assert.Equal(t, 3, tasks[4].ParallelGroup)
	assert.Equal(t, []string{"task-003", "task-004"}, tasks[4].DependsOn)
}

func TestParseTasksFromString_Empty(t *testing.T) {
	tasks, err := ParseTasksFromString("# Tasks\n\nNo tasks yet.")
	require.NoError(t, err)
	assert.Empty(t, tasks)
}

func TestParseTasksFromString_InvalidStatus(t *testing.T) {
	md := `# Tasks

## Task 1: Bad status
- **id**: task-001
- **status**: invalid_status
- **parallel_group**: 1
- **depends_on**: []
- **description**: Test
- **files**: []
`
	_, err := ParseTasksFromString(md)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid status")
}

func TestParseTasksFromString_MissingID(t *testing.T) {
	md := `# Tasks

## Task 1: No ID
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: Test
- **files**: []
`
	_, err := ParseTasksFromString(md)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no id")
}

func TestParseTasksFromString_DefaultStatus(t *testing.T) {
	md := `# Tasks

## Task 1: No explicit status
- **id**: task-001
- **parallel_group**: 1
- **depends_on**: []
- **description**: Test
- **files**: []
`
	tasks, err := ParseTasksFromString(md)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, TaskStatusPending, tasks[0].Status)
}

func TestParseTasks_FromFile(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.md")
	require.NoError(t, os.WriteFile(tasksPath, []byte(sampleTasksMD), 0644))

	tasks, err := ParseTasks(tasksPath)
	require.NoError(t, err)
	assert.Len(t, tasks, 5)
}

func TestUpdateTaskStatus(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.md")
	require.NoError(t, os.WriteFile(tasksPath, []byte(sampleTasksMD), 0644))

	// Update task-003 from pending to running
	err := UpdateTaskStatus(tasksPath, "task-003", TaskStatusRunning)
	require.NoError(t, err)

	// Verify
	tasks, err := ParseTasks(tasksPath)
	require.NoError(t, err)
	for _, task := range tasks {
		if task.ID == "task-003" {
			assert.Equal(t, TaskStatusRunning, task.Status)
		}
	}
}

func TestUpdateTaskStatus_InvalidStatus(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.md")
	require.NoError(t, os.WriteFile(tasksPath, []byte(sampleTasksMD), 0644))

	err := UpdateTaskStatus(tasksPath, "task-001", "bogus")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid status")
}

func TestUpdateTaskStatus_TaskNotFound(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.md")
	require.NoError(t, os.WriteFile(tasksPath, []byte(sampleTasksMD), 0644))

	err := UpdateTaskStatus(tasksPath, "task-999", TaskStatusRunning)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetRunnableTasks(t *testing.T) {
	tasks, err := ParseTasksFromString(sampleTasksMD)
	require.NoError(t, err)

	runnable := GetRunnableTasks(tasks)
	// task-001 and task-002 are completed; task-003 and task-004 depend on them.
	// Both should be runnable since their deps are completed.
	require.Len(t, runnable, 2)
	ids := []string{runnable[0].ID, runnable[1].ID}
	assert.Contains(t, ids, "task-003")
	assert.Contains(t, ids, "task-004")
}

func TestGetRunnableTasks_NoneRunnable(t *testing.T) {
	md := `# Tasks

## Task 1: First
- **id**: task-001
- **status**: running
- **parallel_group**: 1
- **depends_on**: []
- **description**: Test
- **files**: []

## Task 2: Second
- **id**: task-002
- **status**: pending
- **parallel_group**: 2
- **depends_on**: [task-001]
- **description**: Test
- **files**: []
`
	tasks, err := ParseTasksFromString(md)
	require.NoError(t, err)

	runnable := GetRunnableTasks(tasks)
	assert.Empty(t, runnable)
}

func TestGetRunnableTasks_AllCompleted(t *testing.T) {
	md := `# Tasks

## Task 1: First
- **id**: task-001
- **status**: completed
- **parallel_group**: 1
- **depends_on**: []
- **description**: Test
- **files**: []
`
	tasks, err := ParseTasksFromString(md)
	require.NoError(t, err)

	runnable := GetRunnableTasks(tasks)
	assert.Empty(t, runnable)
}

func TestBuildPhases(t *testing.T) {
	tasks, err := ParseTasksFromString(sampleTasksMD)
	require.NoError(t, err)

	phases, err := BuildPhases(tasks)
	require.NoError(t, err)
	require.Len(t, phases, 3)

	// Phase 1: group 1 (task-001, task-002)
	assert.Len(t, phases[0], 2)
	// Phase 2: group 2 (task-003, task-004)
	assert.Len(t, phases[1], 2)
	// Phase 3: group 3 (task-005)
	assert.Len(t, phases[2], 1)
}

func TestBuildPhases_Empty(t *testing.T) {
	phases, err := BuildPhases(nil)
	require.NoError(t, err)
	assert.Nil(t, phases)
}

func TestBuildPhases_UnknownDependency(t *testing.T) {
	md := `# Tasks

## Task 1: First
- **id**: task-001
- **status**: pending
- **parallel_group**: 2
- **depends_on**: [task-999]
- **description**: Test
- **files**: []
`
	tasks, err := ParseTasksFromString(md)
	require.NoError(t, err)

	_, err = BuildPhases(tasks)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown task")
}

func TestBuildPhases_SameGroupDependency(t *testing.T) {
	md := `# Tasks

## Task 1: First
- **id**: task-001
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: Test
- **files**: []

## Task 2: Second
- **id**: task-002
- **status**: pending
- **parallel_group**: 1
- **depends_on**: [task-001]
- **description**: Test
- **files**: []
`
	tasks, err := ParseTasksFromString(md)
	require.NoError(t, err)

	_, err = BuildPhases(tasks)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "dependencies must be in earlier groups")
}

func TestBuildPhases_ForwardDependency(t *testing.T) {
	md := `# Tasks

## Task 1: First
- **id**: task-001
- **status**: pending
- **parallel_group**: 2
- **depends_on**: [task-002]
- **description**: Test
- **files**: []

## Task 2: Second
- **id**: task-002
- **status**: pending
- **parallel_group**: 3
- **depends_on**: []
- **description**: Test
- **files**: []
`
	tasks, err := ParseTasksFromString(md)
	require.NoError(t, err)

	_, err = BuildPhases(tasks)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "dependencies must be in earlier groups")
}

func TestParseJSONStringArray(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "valid JSON array",
			input: `["file1.go", "file2.go"]`,
			want:  []string{"file1.go", "file2.go"},
		},
		{
			name:  "empty JSON array",
			input: `[]`,
			want:  []string{},
		},
		{
			name:  "fallback comma separated",
			input: `file1.go, file2.go`,
			want:  []string{"file1.go", "file2.go"},
		},
		{
			name:  "single file",
			input: `["single.go"]`,
			want:  []string{"single.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseJSONStringArray(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestConcurrentUpdateTaskStatus(t *testing.T) {
	// Build a tasks.md with 8 tasks, all pending.
	var sb strings.Builder
	sb.WriteString("# Tasks\n\n")
	for i := 1; i <= 8; i++ {
		sb.WriteString(fmt.Sprintf("## Task %d: Task number %d\n", i, i))
		sb.WriteString(fmt.Sprintf("- **id**: 1.%d\n", i))
		sb.WriteString("- **status**: pending\n")
		sb.WriteString(fmt.Sprintf("- **parallel_group**: %d\n", i))
		sb.WriteString("- **depends_on**: []\n")
		sb.WriteString(fmt.Sprintf("- **description**: Task %d description\n", i))
		sb.WriteString("- **files**: []\n\n")
	}

	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.md")
	require.NoError(t, os.WriteFile(tasksPath, []byte(sb.String()), 0644))

	// Launch 8 goroutines, each updating one task to in-progress.
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := 1; i <= 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			taskID := fmt.Sprintf("1.%d", idx)
			errs[idx-1] = UpdateTaskStatus(tasksPath, taskID, TaskStatusRunning)
		}(i)
	}
	wg.Wait()

	// Assert no errors from any goroutine.
	for i, err := range errs {
		assert.NoError(t, err, "goroutine %d returned error", i+1)
	}

	// Re-parse and verify all 8 tasks are now running.
	tasks, err := ParseTasks(tasksPath)
	require.NoError(t, err)
	require.Len(t, tasks, 8)

	for _, task := range tasks {
		assert.Equal(t, TaskStatusRunning, task.Status, "task %s should be running", task.ID)
	}
}

func TestParseTasksFromString_WithRetryCount(t *testing.T) {
	md := `# Tasks

## Task 1: Retry example
- **id**: task-001
- **status**: failed
- **parallel_group**: 1
- **depends_on**: []
- **description**: A task that has been retried.
- **files**: ["test.go"]
- **retry_count**: 3
`
	tasks, err := ParseTasksFromString(md)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, 3, tasks[0].RetryCount)
	assert.Equal(t, TaskStatusFailed, tasks[0].Status)
}
