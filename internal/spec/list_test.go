package spec

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpecList_NoSpecs(t *testing.T) {
	// SpecList prints "No specs found." when no specs exist.
	// We can't easily capture stdout in this test, but we verify
	// it doesn't error.
	dir := t.TempDir()
	err := SpecList(&bytes.Buffer{}, dir)
	require.NoError(t, err)
}

func TestSpecList_WithSpecs(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, ".otto", "specs")

	// Create specs with varying artifacts.
	specAlpha := filepath.Join(root, "alpha")
	require.NoError(t, os.MkdirAll(filepath.Join(specAlpha, "history"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(specAlpha, "requirements.md"), []byte("# req"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(specAlpha, "research.md"), []byte("# res"), 0644))

	specBeta := filepath.Join(root, "beta")
	require.NoError(t, os.MkdirAll(filepath.Join(specBeta, "history"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(specBeta, "requirements.md"), []byte("# req"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(specBeta, "research.md"), []byte("# res"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(specBeta, "design.md"), []byte("# des"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(specBeta, "tasks.md"), []byte(`# Tasks

## Task 1: Done
- **id**: task-001
- **status**: completed
- **parallel_group**: 1
- **depends_on**: []
- **description**: Test
- **files**: []

## Task 2: Pending
- **id**: task-002
- **status**: pending
- **parallel_group**: 2
- **depends_on**: [task-001]
- **description**: Test
- **files**: []
`), 0644))

	// Should not error — output goes to writer.
	err := SpecList(&bytes.Buffer{}, dir)
	require.NoError(t, err)
}

func TestTaskProgress(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, ".otto", "specs", "progress-test")
	require.NoError(t, os.MkdirAll(filepath.Join(specDir, "history"), 0755))

	tasks := `# Tasks

## Task 1: Done
- **id**: task-001
- **status**: completed
- **parallel_group**: 1
- **depends_on**: []
- **description**: Test
- **files**: []

## Task 2: Done too
- **id**: task-002
- **status**: completed
- **parallel_group**: 1
- **depends_on**: []
- **description**: Test
- **files**: []

## Task 3: Pending
- **id**: task-003
- **status**: pending
- **parallel_group**: 2
- **depends_on**: [task-001, task-002]
- **description**: Test
- **files**: []
`
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "tasks.md"), []byte(tasks), 0644))

	s, err := LoadSpec("progress-test", dir)
	require.NoError(t, err)

	progress := taskProgress(s)
	assert.Equal(t, "2/3 (66%)", progress)
}

func TestTaskProgress_NoTasks(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, ".otto", "specs", "no-tasks")
	require.NoError(t, os.MkdirAll(filepath.Join(specDir, "history"), 0755))

	s, err := LoadSpec("no-tasks", dir)
	require.NoError(t, err)

	progress := taskProgress(s)
	assert.Equal(t, "-", progress)
}

func TestIndicator(t *testing.T) {
	assert.Equal(t, "✓", indicator(true))
	assert.Equal(t, "·", indicator(false))
}

func TestFormatSpecSummary(t *testing.T) {
	dir := setupSpecDir(t, "summary-test", "requirements.md", "design.md")
	s, err := LoadSpec("summary-test", dir)
	require.NoError(t, err)

	summary := FormatSpecSummary(s)
	assert.Contains(t, summary, "summary-test")
	assert.Contains(t, summary, "R")
	assert.Contains(t, summary, "D")
}

func TestFormatSpecSummary_Empty(t *testing.T) {
	dir := setupSpecDir(t, "empty-summary")
	s, err := LoadSpec("empty-summary", dir)
	require.NoError(t, err)

	summary := FormatSpecSummary(s)
	assert.Contains(t, summary, "empty")
}
