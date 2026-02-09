package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTestFile creates a file with the given number of lines in workDir/filePath.
// Each line reads "Line N content" where N is the 1-based line number.
func makeTestFile(t *testing.T, workDir, filePath string, lineCount int) {
	t.Helper()
	fullPath := filepath.Join(workDir, filePath)
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0755))
	var sb strings.Builder
	for i := 1; i <= lineCount; i++ {
		sb.WriteString(fmt.Sprintf("Line %d content", i))
		if i < lineCount {
			sb.WriteString("\n")
		}
	}
	require.NoError(t, os.WriteFile(fullPath, []byte(sb.String()), 0644))
}

func TestReadCodeContext_MiddleOfFile(t *testing.T) {
	workDir := t.TempDir()
	makeTestFile(t, workDir, "main.go", 20)

	result := readCodeContext(workDir, "main.go", 10, 5)

	// Should contain lines 5 through 15 (line-5=10-5, line+5=10+5).
	assert.Contains(t, result, "Line 5 content")
	assert.Contains(t, result, "Line 10 content")
	assert.Contains(t, result, "Line 15 content")

	// Should not contain lines outside the range.
	assert.NotContains(t, result, "Line 4 content")
	assert.NotContains(t, result, "Line 16 content")

	// Should have line numbers in the output.
	assert.Contains(t, result, "  10 |")
}

func TestReadCodeContext_StartOfFile(t *testing.T) {
	workDir := t.TempDir()
	makeTestFile(t, workDir, "start.go", 20)

	result := readCodeContext(workDir, "start.go", 1, 5)

	// Should start at line 1 (no underflow).
	assert.Contains(t, result, "Line 1 content")
	assert.Contains(t, result, "   1 |")

	// Should include lines up to 1+5=6.
	assert.Contains(t, result, "Line 6 content")

	// Should not be empty.
	assert.NotEmpty(t, result)
}

func TestReadCodeContext_EndOfFile(t *testing.T) {
	workDir := t.TempDir()
	makeTestFile(t, workDir, "end.go", 20)

	result := readCodeContext(workDir, "end.go", 20, 5)

	// Should include the last line.
	assert.Contains(t, result, "Line 20 content")

	// Should include lines from 15 onward (20-5=15).
	assert.Contains(t, result, "Line 15 content")
}

func TestReadCodeContext_NonexistentFile(t *testing.T) {
	workDir := t.TempDir()

	result := readCodeContext(workDir, "does-not-exist.go", 10, 5)

	// Should return empty string without panicking.
	assert.Empty(t, result)
}

func TestReadCodeContext_ZeroLine(t *testing.T) {
	workDir := t.TempDir()
	makeTestFile(t, workDir, "zero.go", 20)

	// line=0 should not panic or produce negative indices.
	result := readCodeContext(workDir, "zero.go", 0, 5)

	// The function should handle gracefully — either empty or some lines from the start.
	// It should not panic.
	_ = result
}
