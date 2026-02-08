package spec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleQuestionsMD = `# Questions

## Q1: Authentication method
- **source**: design (2026-02-06)
- **status**: unanswered
- **question**: What authentication method should we use for the API?
- **answer**: 
- **validated_by**: 

## Q2: Database choice
- **source**: requirements (2026-02-05)
- **status**: answered
- **question**: Which database should we use?
- **answer**: PostgreSQL with pgx driver
- **validated_by**: human

## Q3: Logging library
- **source**: research (2026-02-06)
- **status**: auto-answered
- **question**: Which logging library is best for this project?
- **answer**: slog from the standard library
- **validated_by**: openai/o3
`

func TestParseQuestionsFromString(t *testing.T) {
	questions, err := ParseQuestionsFromString(sampleQuestionsMD)
	require.NoError(t, err)
	require.Len(t, questions, 3)

	// Q1
	assert.Equal(t, "Q1", questions[0].ID)
	assert.Equal(t, "design (2026-02-06)", questions[0].Source)
	assert.Equal(t, QuestionStatusUnanswered, questions[0].Status)
	assert.Equal(t, "What authentication method should we use for the API?", questions[0].QuestionText)
	assert.Empty(t, questions[0].Answer)
	assert.Empty(t, questions[0].ValidatedBy)

	// Q2
	assert.Equal(t, "Q2", questions[1].ID)
	assert.Equal(t, QuestionStatusAnswered, questions[1].Status)
	assert.Equal(t, "PostgreSQL with pgx driver", questions[1].Answer)
	assert.Equal(t, []string{"human"}, questions[1].ValidatedBy)

	// Q3
	assert.Equal(t, "Q3", questions[2].ID)
	assert.Equal(t, QuestionStatusAutoAnswered, questions[2].Status)
	assert.Equal(t, "slog from the standard library", questions[2].Answer)
	assert.Equal(t, []string{"openai/o3"}, questions[2].ValidatedBy)
}

func TestParseQuestionsFromString_Empty(t *testing.T) {
	questions, err := ParseQuestionsFromString("# Questions\n\nNo questions yet.")
	require.NoError(t, err)
	assert.Empty(t, questions)
}

func TestParseQuestionsFromString_DefaultStatus(t *testing.T) {
	md := `# Questions

## Q1: Missing status
- **source**: test
- **question**: What is this?
- **answer**: 
- **validated_by**: 
`
	questions, err := ParseQuestionsFromString(md)
	require.NoError(t, err)
	require.Len(t, questions, 1)
	assert.Equal(t, QuestionStatusUnanswered, questions[0].Status)
}

func TestParseQuestions_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "questions.md")
	require.NoError(t, os.WriteFile(path, []byte(sampleQuestionsMD), 0644))

	questions, err := ParseQuestions(path)
	require.NoError(t, err)
	assert.Len(t, questions, 3)
}

func TestWriteQuestions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "questions.md")

	questions := []Question{
		{
			ID:           "Q1",
			Source:       "design (2026-02-06)",
			Status:       QuestionStatusUnanswered,
			QuestionText: "What should we use?",
			Answer:       "",
			ValidatedBy:  []string{},
		},
		{
			ID:           "Q2",
			Source:       "research",
			Status:       QuestionStatusAutoAnswered,
			QuestionText: "Which library?",
			Answer:       "slog",
			ValidatedBy:  []string{"openai/o3"},
		},
	}

	err := WriteQuestions(path, questions)
	require.NoError(t, err)

	// Read back and parse.
	parsed, err := ParseQuestions(path)
	require.NoError(t, err)
	require.Len(t, parsed, 2)

	assert.Equal(t, "Q1", parsed[0].ID)
	assert.Equal(t, QuestionStatusUnanswered, parsed[0].Status)
	assert.Equal(t, "What should we use?", parsed[0].QuestionText)

	assert.Equal(t, "Q2", parsed[1].ID)
	assert.Equal(t, QuestionStatusAutoAnswered, parsed[1].Status)
	assert.Equal(t, "slog", parsed[1].Answer)
	assert.Equal(t, []string{"openai/o3"}, parsed[1].ValidatedBy)
}

func TestWriteQuestions_Roundtrip(t *testing.T) {
	// Parse the sample, write it, parse again — should match.
	questions, err := ParseQuestionsFromString(sampleQuestionsMD)
	require.NoError(t, err)

	dir := t.TempDir()
	path := filepath.Join(dir, "questions.md")

	err = WriteQuestions(path, questions)
	require.NoError(t, err)

	roundtripped, err := ParseQuestions(path)
	require.NoError(t, err)

	require.Len(t, roundtripped, len(questions))
	for i := range questions {
		assert.Equal(t, questions[i].ID, roundtripped[i].ID)
		assert.Equal(t, questions[i].Status, roundtripped[i].Status)
		assert.Equal(t, questions[i].QuestionText, roundtripped[i].QuestionText)
		assert.Equal(t, questions[i].Answer, roundtripped[i].Answer)
	}
}

func TestParseQuestionsFromString_MultipleValidators(t *testing.T) {
	md := `# Questions

## Q1: Multi-validated
- **source**: design
- **status**: auto-answered
- **question**: Which approach to use?
- **answer**: Use approach A
- **validated_by**: openai/o3, google/gemini-2.5-pro
`
	questions, err := ParseQuestionsFromString(md)
	require.NoError(t, err)
	require.Len(t, questions, 1)
	assert.Equal(t, []string{"openai/o3", "google/gemini-2.5-pro"}, questions[0].ValidatedBy)
}
