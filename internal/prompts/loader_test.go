package prompts

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var expectedTemplates = []string{
	"design.md",
	"phase-review.md",
	"pr-comment-respond.md",
	"pr-review.md",
	"question-harvest.md",
	"question-resolve.md",
	"requirements.md",
	"research.md",
	"review.md",
	"tasks.md",
}

func TestLoadAllTemplates(t *testing.T) {
	for _, name := range expectedTemplates {
		t.Run(name, func(t *testing.T) {
			tmpl, err := Load(name)
			require.NoError(t, err)
			assert.NotNil(t, tmpl)
		})
	}
}

func TestLoadNonExistent(t *testing.T) {
	_, err := Load("nonexistent-template.md")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "loading prompt template")
}

func TestList(t *testing.T) {
	names, err := List()
	require.NoError(t, err)

	// Should contain all 10 .md files
	assert.GreaterOrEqual(t, len(names), 10)
	for _, expected := range expectedTemplates {
		assert.Contains(t, names, expected)
	}
}

func TestExecuteDesignTemplate(t *testing.T) {
	data := map[string]string{
		"requirements_md": "Build a CLI tool.",
		"research_md":     "Use cobra for CLI framework.",
	}

	result, err := Execute("design.md", data)
	require.NoError(t, err)

	// The template should have substituted the values
	assert.Contains(t, result, "Build a CLI tool.")
	assert.Contains(t, result, "Use cobra for CLI framework.")
	// Should contain static content from the template
	assert.Contains(t, result, "Design Phase")
}

func TestExecuteRequirementsTemplate(t *testing.T) {
	data := map[string]string{
		"requirements_md": "# My Requirements\n\nBuild something great.",
	}

	result, err := Execute("requirements.md", data)
	require.NoError(t, err)

	assert.Contains(t, result, "My Requirements")
	assert.True(t, len(result) > 100, "result should contain template content plus substituted data")
}

func TestExecuteWithEmptyData(t *testing.T) {
	// Should work with empty data map — template vars become zero-values
	result, err := Execute("research.md", map[string]string{})
	require.NoError(t, err)
	assert.True(t, len(strings.TrimSpace(result)) > 0)
}
