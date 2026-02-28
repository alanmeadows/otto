package prompts

import (
"testing"

"github.com/stretchr/testify/assert"
"github.com/stretchr/testify/require"
)

var expectedTemplates = []string{
"merlinbot-evaluate.md",
"pr-comment-respond.md",
"pr-description.md",
"pr-review.md",
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

assert.Equal(t, len(expectedTemplates), len(names))
for _, expected := range expectedTemplates {
assert.Contains(t, names, expected)
}
}

func TestExecutePRReviewTemplate(t *testing.T) {
data := map[string]string{
"pr_title":      "Add retry logic",
"pr_description": "This PR adds retry.",
"target_branch":  "main",
}

result, err := Execute("pr-review.md", data)
require.NoError(t, err)
assert.Contains(t, result, "Add retry logic")
assert.Contains(t, result, "main")
}

func TestExecutePRDescriptionTemplate(t *testing.T) {
data := map[string]string{
"BranchName": "feature/retry",
"CommitLog":  "abc123 Add retry logic",
}

result, err := Execute("pr-description.md", data)
require.NoError(t, err)
assert.Contains(t, result, "feature/retry")
assert.Contains(t, result, "abc123")
}

func TestExecuteCommentRespondTemplate(t *testing.T) {
data := map[string]string{
"pr_title":       "Fix bug",
"comment_author":  "reviewer",
"comment_file":    "main.go",
"comment_line":    "42",
"comment_body":    "Missing nil check",
"code_context":    "if err != nil {",
}

result, err := Execute("pr-comment-respond.md", data)
require.NoError(t, err)
assert.Contains(t, result, "Missing nil check")
assert.Contains(t, result, "main.go")
}

func TestExecuteMerlinbotTemplate(t *testing.T) {
data := map[string]string{
"Comments": "Thread 1: Security issue found",
}

result, err := Execute("merlinbot-evaluate.md", data)
require.NoError(t, err)
assert.Contains(t, result, "Security issue found")
}
