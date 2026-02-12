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

// --- SplitQuestions tests ---

func TestSplitQuestions_WithSeparator(t *testing.T) {
	input := "# Research Output\n\nSome content here.\n\n===QUESTIONS===\n\n## Q1: Something\n- **source**: research\n- **status**: unanswered\n- **question**: What is this?"
	artifact, questions := SplitQuestions(input)

	assert.Equal(t, "# Research Output\n\nSome content here.", artifact)
	assert.Contains(t, questions, "## Q1: Something")
	assert.Contains(t, questions, "research")
}

func TestSplitQuestions_NoSeparator(t *testing.T) {
	input := "# Research Output\n\nSome content, no questions."
	artifact, questions := SplitQuestions(input)

	assert.Equal(t, input, artifact)
	assert.Empty(t, questions)
}

func TestSplitQuestions_EmptyQuestions(t *testing.T) {
	input := "# Content\n\n===QUESTIONS===\n\n"
	artifact, questions := SplitQuestions(input)

	assert.Equal(t, "# Content", artifact)
	assert.Empty(t, questions)
}

func TestSplitQuestions_MultipleSeparators(t *testing.T) {
	// Only splits on the first separator.
	input := "Part A\n===QUESTIONS===\nPart B\n===QUESTIONS===\nPart C"
	artifact, questions := SplitQuestions(input)

	assert.Equal(t, "Part A", artifact)
	assert.Contains(t, questions, "Part B")
	assert.Contains(t, questions, "===QUESTIONS===")
	assert.Contains(t, questions, "Part C")
}

// --- maxQuestionNum tests ---

func TestMaxQuestionNum(t *testing.T) {
	questions := []Question{
		{ID: "Q1"}, {ID: "Q5"}, {ID: "Q3"},
	}
	assert.Equal(t, 5, maxQuestionNum(questions))
}

func TestMaxQuestionNum_Empty(t *testing.T) {
	assert.Equal(t, 0, maxQuestionNum(nil))
	assert.Equal(t, 0, maxQuestionNum([]Question{}))
}

func TestMaxQuestionNum_NonStandard(t *testing.T) {
	// Questions with non-parseable IDs are silently ignored.
	questions := []Question{
		{ID: "Q2"}, {ID: "QX"}, {ID: "Q10"},
	}
	assert.Equal(t, 10, maxQuestionNum(questions))
}

// --- ExtractAndAppendQuestions tests ---

func TestExtractAndAppendQuestions_NewFile(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, ".otto", "specs", "test-eaq")
	require.NoError(t, os.MkdirAll(specDir, 0755))

	spec := &Spec{
		Slug:          "test-eaq",
		Dir:           specDir,
		QuestionsPath: filepath.Join(specDir, "questions.md"),
	}

	output := "# Content\n\n===QUESTIONS===\n\n## Q1: New question\n- **source**: research\n- **status**: unanswered\n- **question**: Is this working?\n- **answer**: \n- **validated_by**: "

	ExtractAndAppendQuestions(output, spec)

	// Check questions.md was created.
	questions, err := ParseQuestions(spec.QuestionsPath)
	require.NoError(t, err)
	require.Len(t, questions, 1)
	assert.Equal(t, "Q1", questions[0].ID)
	assert.Equal(t, "New question", questions[0].Title)
	assert.Equal(t, "unanswered", questions[0].Status)
}

func TestExtractAndAppendQuestions_Renumbering(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, ".otto", "specs", "test-renum")
	require.NoError(t, os.MkdirAll(specDir, 0755))

	spec := &Spec{
		Slug:          "test-renum",
		Dir:           specDir,
		QuestionsPath: filepath.Join(specDir, "questions.md"),
	}

	// Write existing questions.
	existing := []Question{
		{ID: "Q1", Title: "Existing", Source: "requirements", Status: "answered", QuestionText: "Old?", Answer: "Yes", ValidatedBy: []string{"human"}},
		{ID: "Q2", Title: "Also existing", Source: "requirements", Status: "unanswered", QuestionText: "Another?", ValidatedBy: []string{}},
	}
	require.NoError(t, WriteQuestions(spec.QuestionsPath, existing))

	// New questions from LLM output use Q1, Q2 (would collide).
	output := "===QUESTIONS===\n\n## Q1: Brand new\n- **source**: design\n- **status**: unanswered\n- **question**: What now?\n- **answer**: \n- **validated_by**: \n\n## Q2: Another new\n- **source**: design\n- **status**: unanswered\n- **question**: And this?\n- **answer**: \n- **validated_by**: "

	ExtractAndAppendQuestions(output, spec)

	// Should now have 4 questions, new ones renumbered to Q3, Q4.
	questions, err := ParseQuestions(spec.QuestionsPath)
	require.NoError(t, err)
	require.Len(t, questions, 4)
	assert.Equal(t, "Q1", questions[0].ID)
	assert.Equal(t, "Q2", questions[1].ID)
	assert.Equal(t, "Q3", questions[2].ID)
	assert.Equal(t, "Brand new", questions[2].Title)
	assert.Equal(t, "Q4", questions[3].ID)
	assert.Equal(t, "Another new", questions[3].Title)
}

func TestExtractAndAppendQuestions_NoSeparator(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, ".otto", "specs", "test-nosep")
	require.NoError(t, os.MkdirAll(specDir, 0755))

	spec := &Spec{
		Slug:          "test-nosep",
		Dir:           specDir,
		QuestionsPath: filepath.Join(specDir, "questions.md"),
	}

	output := "Just content, no questions separator."
	ExtractAndAppendQuestions(output, spec)

	// questions.md should not exist.
	_, err := os.Stat(spec.QuestionsPath)
	assert.True(t, os.IsNotExist(err))
}

// --- CheckUnansweredQuestions tests ---

func TestCheckUnansweredQuestions(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, ".otto", "specs", "test-check")
	require.NoError(t, os.MkdirAll(specDir, 0755))

	spec := &Spec{
		Slug:          "test-check",
		Dir:           specDir,
		QuestionsPath: filepath.Join(specDir, "questions.md"),
	}

	// No questions file → 0.
	count, err := CheckUnansweredQuestions(spec)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Write questions with mixed statuses.
	questions := []Question{
		{ID: "Q1", Title: "A", Source: "req", Status: QuestionStatusUnanswered, QuestionText: "?", ValidatedBy: []string{}},
		{ID: "Q2", Title: "B", Source: "req", Status: QuestionStatusAnswered, QuestionText: "?", Answer: "yes", ValidatedBy: []string{"human"}},
		{ID: "Q3", Title: "C", Source: "req", Status: QuestionStatusUnanswered, QuestionText: "?", ValidatedBy: []string{}},
		{ID: "Q4", Title: "D", Source: "req", Status: QuestionStatusAutoAnswered, QuestionText: "?", Answer: "maybe", ValidatedBy: []string{"model"}},
	}
	require.NoError(t, WriteQuestions(spec.QuestionsPath, questions))

	count, err = CheckUnansweredQuestions(spec)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

// --- UnansweredQuestionTitles tests ---

func TestUnansweredQuestionTitles(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, ".otto", "specs", "test-titles")
	require.NoError(t, os.MkdirAll(specDir, 0755))

	spec := &Spec{
		Slug:          "test-titles",
		Dir:           specDir,
		QuestionsPath: filepath.Join(specDir, "questions.md"),
	}

	// No file → nil.
	assert.Nil(t, UnansweredQuestionTitles(spec))

	questions := []Question{
		{ID: "Q1", Title: "Auth method", Source: "req", Status: QuestionStatusUnanswered, QuestionText: "?", ValidatedBy: []string{}},
		{ID: "Q2", Title: "DB choice", Source: "req", Status: QuestionStatusAnswered, QuestionText: "?", Answer: "pg", ValidatedBy: []string{"human"}},
		{ID: "Q3", Title: "Logging lib", Source: "req", Status: QuestionStatusUnanswered, QuestionText: "?", ValidatedBy: []string{}},
	}
	require.NoError(t, WriteQuestions(spec.QuestionsPath, questions))

	titles := UnansweredQuestionTitles(spec)
	require.Len(t, titles, 2)
	assert.Equal(t, "Q1: Auth method", titles[0])
	assert.Equal(t, "Q3: Logging lib", titles[1])
}

// --- appendRawQuestions tests ---

func TestAppendRawQuestions_NewFile(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, ".otto", "specs", "test-raw")
	require.NoError(t, os.MkdirAll(specDir, 0755))

	spec := &Spec{
		Slug:          "test-raw",
		Dir:           specDir,
		QuestionsPath: filepath.Join(specDir, "questions.md"),
	}

	appendRawQuestions("## Some raw question content", spec)

	content, err := os.ReadFile(spec.QuestionsPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "Some raw question content")
}

func TestAppendRawQuestions_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, ".otto", "specs", "test-raw-exist")
	require.NoError(t, os.MkdirAll(specDir, 0755))

	spec := &Spec{
		Slug:          "test-raw-exist",
		Dir:           specDir,
		QuestionsPath: filepath.Join(specDir, "questions.md"),
	}

	// Write initial content.
	require.NoError(t, os.WriteFile(spec.QuestionsPath, []byte("# Questions\n\nExisting content"), 0644))

	appendRawQuestions("## New raw content", spec)

	content, err := os.ReadFile(spec.QuestionsPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "Existing content")
	assert.Contains(t, string(content), "New raw content")
}
