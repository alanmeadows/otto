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

// TestExecuteAllTemplatesWithFullData parses every template with all variables
// populated, asserts no parse errors, and verifies the output contains the
// substituted values.
func TestExecuteAllTemplatesWithFullData(t *testing.T) {
	// Full data map with every known template variable set to a unique non-empty string.
	fullData := map[string]string{
		"requirements_md":      "REQUIREMENTS_CONTENT_PLACEHOLDER",
		"research_md":          "RESEARCH_CONTENT_PLACEHOLDER",
		"design_md":            "DESIGN_CONTENT_PLACEHOLDER",
		"existing_design_md":   "EXISTING_DESIGN_PLACEHOLDER",
		"existing_research_md": "EXISTING_RESEARCH_PLACEHOLDER",
		"existing_tasks_md":    "EXISTING_TASKS_PLACEHOLDER",
		"questions_md":         "QUESTIONS_CONTENT_PLACEHOLDER",
		"existing_artifacts":   "EXISTING_ARTIFACTS_PLACEHOLDER",
		"codebase_summary":     "CODEBASE_SUMMARY_PLACEHOLDER",
		"artifact":             "ARTIFACT_CONTENT_PLACEHOLDER",
		"context":              "CONTEXT_CONTENT_PLACEHOLDER",
		"execution_logs":       "EXECUTION_LOGS_PLACEHOLDER",
		"phase_number":         "PHASE_NUMBER_PLACEHOLDER",
		"phase_summaries":      "PHASE_SUMMARIES_PLACEHOLDER",
		"pr_title":             "PR_TITLE_PLACEHOLDER",
		"pr_description":       "PR_DESCRIPTION_PLACEHOLDER",
		"target_branch":        "TARGET_BRANCH_PLACEHOLDER",
		"comment_author":       "COMMENT_AUTHOR_PLACEHOLDER",
		"comment_file":         "COMMENT_FILE_PLACEHOLDER",
		"comment_line":         "COMMENT_LINE_PLACEHOLDER",
		"comment_body":         "COMMENT_BODY_PLACEHOLDER",
		"comment_thread":       "COMMENT_THREAD_PLACEHOLDER",
		"code_context":         "CODE_CONTEXT_PLACEHOLDER",
		"tasks_md":             "TASKS_MD_PLACEHOLDER",
		"question":             "QUESTION_PLACEHOLDER",
		"spec_context":         "SPEC_CONTEXT_PLACEHOLDER",
	}

	// Per-template expected variables: which placeholders should appear in the output.
	templateExpected := map[string][]string{
		"requirements.md": {
			"REQUIREMENTS_CONTENT_PLACEHOLDER",
			"QUESTIONS_CONTENT_PLACEHOLDER",
			"CODEBASE_SUMMARY_PLACEHOLDER",
			"EXISTING_ARTIFACTS_PLACEHOLDER",
		},
		"research.md": {
			"REQUIREMENTS_CONTENT_PLACEHOLDER",
			"CODEBASE_SUMMARY_PLACEHOLDER",
			"EXISTING_RESEARCH_PLACEHOLDER",
		},
		"design.md": {
			"REQUIREMENTS_CONTENT_PLACEHOLDER",
			"RESEARCH_CONTENT_PLACEHOLDER",
			"CODEBASE_SUMMARY_PLACEHOLDER",
			"EXISTING_DESIGN_PLACEHOLDER",
			"TASKS_MD_PLACEHOLDER",
			"QUESTIONS_CONTENT_PLACEHOLDER",
		},
		"tasks.md": {
			"REQUIREMENTS_CONTENT_PLACEHOLDER",
			"RESEARCH_CONTENT_PLACEHOLDER",
			"DESIGN_CONTENT_PLACEHOLDER",
			"CODEBASE_SUMMARY_PLACEHOLDER",
			"EXISTING_TASKS_PLACEHOLDER",
			"PHASE_SUMMARIES_PLACEHOLDER",
		},
		"review.md": {
			"ARTIFACT_CONTENT_PLACEHOLDER",
			"CONTEXT_CONTENT_PLACEHOLDER",
		},
		"phase-review.md": {
			"PHASE_SUMMARIES_PLACEHOLDER",
		},
		"question-harvest.md": {
			"EXECUTION_LOGS_PLACEHOLDER",
			"PHASE_NUMBER_PLACEHOLDER",
			"PHASE_SUMMARIES_PLACEHOLDER",
		},
		"question-resolve.md": {
			"QUESTION_PLACEHOLDER",
			"CONTEXT_CONTENT_PLACEHOLDER",
			"SPEC_CONTEXT_PLACEHOLDER",
		},
		"pr-review.md": {
			"PR_TITLE_PLACEHOLDER",
			"PR_DESCRIPTION_PLACEHOLDER",
			"TARGET_BRANCH_PLACEHOLDER",
			"CODEBASE_SUMMARY_PLACEHOLDER",
		},
		"pr-comment-respond.md": {
			"PR_TITLE_PLACEHOLDER",
			"PR_DESCRIPTION_PLACEHOLDER",
			"COMMENT_AUTHOR_PLACEHOLDER",
			"COMMENT_FILE_PLACEHOLDER",
			"COMMENT_LINE_PLACEHOLDER",
			"COMMENT_BODY_PLACEHOLDER",
			"COMMENT_THREAD_PLACEHOLDER",
			"CODE_CONTEXT_PLACEHOLDER",
		},
	}

	for _, name := range expectedTemplates {
		t.Run(name, func(t *testing.T) {
			result, err := Execute(name, fullData)
			require.NoError(t, err, "template %s failed to execute", name)
			assert.NotEmpty(t, result, "template %s produced empty output", name)

			// Check that expected substitutions appear in the output.
			expected, ok := templateExpected[name]
			require.True(t, ok, "no expected variables defined for template %s", name)
			for _, placeholder := range expected {
				assert.Contains(t, result, placeholder,
					"template %s should contain substituted value %q", name, placeholder)
			}
		})
	}
}
