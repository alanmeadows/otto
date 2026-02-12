package spec

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/opencode"
	"github.com/alanmeadows/otto/internal/prompts"
	"github.com/alanmeadows/otto/internal/store"
)

// Question represents a single question parsed from questions.md.
type Question struct {
	ID           string
	Title        string
	Source       string
	Status       string
	QuestionText string
	Answer       string
	ValidatedBy  []string
}

// Valid question statuses.
const (
	QuestionStatusUnanswered   = "unanswered"
	QuestionStatusAutoAnswered = "auto-answered"
	QuestionStatusAnswered     = "answered"
)

// Regex patterns for parsing question fields from markdown.
var (
	questionHeaderRe = regexp.MustCompile(`^##\s+(Q\d+):\s*(.+)$`)
	questionSourceRe = regexp.MustCompile(`^\s*-\s+\*\*source\*\*:\s*(.+)$`)
	questionStatusRe = regexp.MustCompile(`^\s*-\s+\*\*status\*\*:\s*(.+)$`)
	questionTextRe   = regexp.MustCompile(`^\s*-\s+\*\*question\*\*:\s*(.*)$`)
	questionAnswerRe = regexp.MustCompile(`^\s*-\s+\*\*answer\*\*:\s*(.*)$`)
	questionValidRe  = regexp.MustCompile(`^\s*-\s+\*\*validated_by\*\*:\s*(.*)$`)
)

// ParseQuestions parses questions from a questions.md file.
func ParseQuestions(questionsPath string) ([]Question, error) {
	content, err := store.ReadBody(questionsPath)
	if err != nil {
		return nil, fmt.Errorf("reading questions file: %w", err)
	}
	return ParseQuestionsFromString(content)
}

// ParseQuestionsFromString parses questions from a markdown string.
func ParseQuestionsFromString(content string) ([]Question, error) {
	lines := strings.Split(content, "\n")
	var questions []Question
	var current *Question

	for _, line := range lines {
		if m := questionHeaderRe.FindStringSubmatch(line); m != nil {
			if current != nil {
				questions = append(questions, *current)
			}
			current = &Question{
				ID:          m[1],
				Title:       strings.TrimSpace(m[2]),
				ValidatedBy: []string{},
			}
			continue
		}

		if current == nil {
			continue
		}

		if m := questionSourceRe.FindStringSubmatch(line); m != nil {
			current.Source = strings.TrimSpace(m[1])
			continue
		}

		if m := questionStatusRe.FindStringSubmatch(line); m != nil {
			current.Status = strings.TrimSpace(m[1])
			continue
		}

		if m := questionTextRe.FindStringSubmatch(line); m != nil {
			current.QuestionText = strings.TrimSpace(m[1])
			continue
		}

		if m := questionAnswerRe.FindStringSubmatch(line); m != nil {
			current.Answer = strings.TrimSpace(m[1])
			continue
		}

		if m := questionValidRe.FindStringSubmatch(line); m != nil {
			raw := strings.TrimSpace(m[1])
			if raw != "" {
				parts := strings.Split(raw, ",")
				var validators []string
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if p != "" {
						validators = append(validators, p)
					}
				}
				current.ValidatedBy = validators
			}
			continue
		}
	}

	// Don't forget the last question.
	if current != nil {
		questions = append(questions, *current)
	}

	// Default empty statuses to unanswered.
	for i := range questions {
		if questions[i].Status == "" {
			questions[i].Status = QuestionStatusUnanswered
		}
	}

	return questions, nil
}

// WriteQuestions writes questions back to a questions.md file.
func WriteQuestions(questionsPath string, questions []Question) error {
	var buf strings.Builder
	buf.WriteString("# Questions\n\n")
	for _, q := range questions {
		title := q.Title
		if title == "" {
			title = "Question"
		}
		buf.WriteString(fmt.Sprintf("## %s: %s\n", q.ID, title))
		buf.WriteString(fmt.Sprintf("- **source**: %s\n", q.Source))
		buf.WriteString(fmt.Sprintf("- **status**: %s\n", q.Status))
		buf.WriteString(fmt.Sprintf("- **question**: %s\n", q.QuestionText))
		buf.WriteString(fmt.Sprintf("- **answer**: %s\n", q.Answer))
		validatedBy := strings.Join(q.ValidatedBy, ", ")
		buf.WriteString(fmt.Sprintf("- **validated_by**: %s\n", validatedBy))
		buf.WriteString("\n")
	}
	return store.WriteBody(questionsPath, buf.String())
}

// SplitQuestions splits LLM output on the ===QUESTIONS=== separator.
// Returns (artifact content, questions content). If no separator is found,
// the full output is returned as artifact content with empty questions.
func SplitQuestions(output string) (artifact, questions string) {
	parts := strings.SplitN(output, "===QUESTIONS===", 2)
	if len(parts) < 2 {
		return output, ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// ExtractAndAppendQuestions looks for a ===QUESTIONS=== separator in the output
// and appends any questions found to the spec's questions.md.
// It renumbers incoming question IDs to avoid collisions with existing questions.
func ExtractAndAppendQuestions(output string, spec *Spec) {
	_, rawQuestions := SplitQuestions(output)
	if rawQuestions == "" {
		return
	}

	// Parse incoming questions for renumbering.
	incoming, err := ParseQuestionsFromString(rawQuestions)
	if err != nil || len(incoming) == 0 {
		// If parsing fails, still append raw content as fallback.
		appendRawQuestions(rawQuestions, spec)
		return
	}

	// Read existing questions to find the next available ID.
	nextNum := 1
	if spec.HasQuestions() {
		existing, parseErr := ParseQuestions(spec.QuestionsPath)
		if parseErr == nil {
			nextNum = maxQuestionNum(existing) + 1
		}
	}

	// Renumber incoming questions.
	for i := range incoming {
		incoming[i].ID = fmt.Sprintf("Q%d", nextNum)
		nextNum++
	}

	// Merge: read all existing, append incoming, write back.
	var all []Question
	if spec.HasQuestions() {
		existing, parseErr := ParseQuestions(spec.QuestionsPath)
		if parseErr == nil {
			all = existing
		}
	}
	all = append(all, incoming...)

	if err := WriteQuestions(spec.QuestionsPath, all); err != nil {
		slog.Warn("failed to write questions", "error", err)
	}
}

// appendRawQuestions appends raw question markdown as a fallback when parsing fails.
func appendRawQuestions(raw string, spec *Spec) {
	var existing string
	if spec.HasQuestions() {
		if content, err := store.ReadBody(spec.QuestionsPath); err == nil {
			existing = content
		}
	}

	var combined string
	if existing != "" {
		combined = existing + "\n\n" + raw
	} else {
		combined = raw
	}

	if err := store.WriteBody(spec.QuestionsPath, combined); err != nil {
		slog.Warn("failed to write questions", "error", err)
	}
}

// maxQuestionNum finds the highest question number in a slice of questions.
func maxQuestionNum(questions []Question) int {
	max := 0
	for _, q := range questions {
		var n int
		if _, err := fmt.Sscanf(q.ID, "Q%d", &n); err == nil && n > max {
			max = n
		}
	}
	return max
}

// CheckUnansweredQuestions returns the count of unanswered questions for a spec.
// Returns 0 if no questions.md exists.
func CheckUnansweredQuestions(spec *Spec) (int, error) {
	if !spec.HasQuestions() {
		return 0, nil
	}

	questions, err := ParseQuestions(spec.QuestionsPath)
	if err != nil {
		return 0, fmt.Errorf("parsing questions: %w", err)
	}

	count := 0
	for _, q := range questions {
		if q.Status == QuestionStatusUnanswered {
			count++
		}
	}
	return count, nil
}

// UnansweredQuestionTitles returns the titles of all unanswered questions.
func UnansweredQuestionTitles(spec *Spec) []string {
	if !spec.HasQuestions() {
		return nil
	}
	questions, err := ParseQuestions(spec.QuestionsPath)
	if err != nil {
		return nil
	}
	var titles []string
	for _, q := range questions {
		if q.Status == QuestionStatusUnanswered {
			titles = append(titles, fmt.Sprintf("%s: %s", q.ID, q.Title))
		}
	}
	return titles
}

// ResolveAndReport runs auto-resolution and prints a summary.
// Returns the number of remaining unanswered questions.
func ResolveAndReport(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	spec *Spec,
	phaseLabel string,
) int {
	if !spec.HasQuestions() {
		return 0
	}

	// Count questions before resolution.
	questionsBefore, err := ParseQuestions(spec.QuestionsPath)
	if err != nil {
		slog.Warn("failed to parse questions for auto-resolve", "error", err)
		return 0
	}

	unansweredBefore := 0
	for _, q := range questionsBefore {
		if q.Status == QuestionStatusUnanswered {
			unansweredBefore++
		}
	}

	if unansweredBefore == 0 {
		return 0
	}

	fmt.Fprintf(os.Stderr, "  ⏳ Auto-resolving %d question(s) from %s...\n", unansweredBefore, phaseLabel)

	// Run auto-resolution.
	if err := autoResolveQuestions(ctx, client, cfg, repoDir, spec); err != nil {
		slog.Warn("auto-resolution failed", "error", err)
	}

	// Count after resolution.
	questionsAfter, err := ParseQuestions(spec.QuestionsPath)
	if err != nil {
		return unansweredBefore
	}

	unansweredAfter := 0
	for _, q := range questionsAfter {
		if q.Status == QuestionStatusUnanswered {
			unansweredAfter++
		}
	}

	resolved := unansweredBefore - unansweredAfter
	fmt.Fprintf(os.Stderr, "  ✓ Questions: %d generated, %d auto-resolved, %d remaining\n",
		unansweredBefore, resolved, unansweredAfter)

	if unansweredAfter > 0 {
		titles := UnansweredQuestionTitles(spec)
		for _, t := range titles {
			fmt.Fprintf(os.Stderr, "    • %s\n", t)
		}
	}

	return unansweredAfter
}

// SpecQuestions performs auto-resolution of unanswered questions using the LLM,
// then runs interactive resolution for any remaining unanswered questions.
func SpecQuestions(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	slug string,
	autoOnly bool,
) error {
	spec, err := ResolveSpec(slug, repoDir)
	if err != nil {
		return err
	}

	if !spec.HasQuestions() {
		return fmt.Errorf("no questions.md found — questions are generated during other pipeline stages")
	}

	// Step 1: Auto-resolve.
	if err := autoResolveQuestions(ctx, client, cfg, repoDir, spec); err != nil {
		return err
	}

	// Step 2: Interactive resolve (if not --auto-only and stdin is a terminal).
	if !autoOnly {
		if err := InteractiveResolve(spec); err != nil {
			return fmt.Errorf("interactive resolution: %w", err)
		}
	}

	return nil
}

// autoResolveQuestions performs LLM-based auto-resolution of unanswered questions.
func autoResolveQuestions(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	spec *Spec,
) error {
	questions, err := ParseQuestions(spec.QuestionsPath)
	if err != nil {
		return fmt.Errorf("parsing questions: %w", err)
	}

	// Gather spec context for the question-resolve prompt.
	requirementsMD := readArtifact(spec.RequirementsPath)
	researchMD := readArtifact(spec.ResearchPath)
	designMD := readArtifact(spec.DesignPath)

	var specContext strings.Builder
	if requirementsMD != "" {
		fmt.Fprintf(&specContext, "### Requirements\n\n%s\n\n", requirementsMD)
	}
	if researchMD != "" {
		fmt.Fprintf(&specContext, "### Research\n\n%s\n\n", researchMD)
	}
	if designMD != "" {
		fmt.Fprintf(&specContext, "### Design\n\n%s\n\n", designMD)
	}

	primaryModel := opencode.ParseModelRef(cfg.Models.Primary)
	secondaryModel := opencode.ParseModelRef(cfg.Models.Secondary)

	autoAnswered := 0
	remaining := 0

	for i, q := range questions {
		if q.Status != QuestionStatusUnanswered {
			continue
		}

		slog.Info("attempting to auto-resolve question", "id", q.ID, "question", q.QuestionText)

		// Step 1: Ask primary model to answer the question.
		answer, err := resolveQuestion(ctx, client, primaryModel, repoDir, q.QuestionText, specContext.String())
		if err != nil {
			slog.Warn("failed to resolve question", "id", q.ID, "error", err)
			remaining++
			continue
		}

		if answer == "CANNOT_RESOLVE" {
			slog.Info("question cannot be auto-resolved", "id", q.ID)
			remaining++
			continue
		}

		// Step 2: Validate with secondary model.
		validated, err := validateAnswer(ctx, client, secondaryModel, repoDir, q.QuestionText, answer)
		if err != nil {
			slog.Warn("validation failed", "id", q.ID, "error", err)
			remaining++
			continue
		}

		if validated {
			questions[i].Status = QuestionStatusAutoAnswered
			questions[i].Answer = answer
			questions[i].ValidatedBy = []string{secondaryModel.String()}
			autoAnswered++
			slog.Info("question auto-answered", "id", q.ID)
		} else {
			slog.Info("answer not validated, keeping as unanswered", "id", q.ID)
			remaining++
		}
	}

	// Write updated questions.
	if err := WriteQuestions(spec.QuestionsPath, questions); err != nil {
		return fmt.Errorf("writing questions: %w", err)
	}

	slog.Info("question resolution complete", "auto_answered", autoAnswered, "remaining", remaining)
	return nil
}

// InteractiveResolve prompts the user to answer unanswered questions interactively.
// Questions the user leaves blank are skipped (remain unanswered).
// Only runs when stdin is a terminal.
func InteractiveResolve(spec *Spec) error {
	// Check if stdin is a terminal.
	info, err := os.Stdin.Stat()
	if err != nil || (info.Mode()&os.ModeCharDevice) == 0 {
		slog.Debug("stdin is not a terminal, skipping interactive resolution")
		return nil
	}

	questions, err := ParseQuestions(spec.QuestionsPath)
	if err != nil {
		return fmt.Errorf("parsing questions: %w", err)
	}

	// Find unanswered questions.
	unanswered := 0
	for _, q := range questions {
		if q.Status == QuestionStatusUnanswered {
			unanswered++
		}
	}

	if unanswered == 0 {
		return nil
	}

	fmt.Fprintf(os.Stderr, "\n%d unanswered question(s). Enter answers below (blank to skip):\n\n", unanswered)

	scanner := bufio.NewScanner(os.Stdin)
	modified := false

	for i, q := range questions {
		if q.Status != QuestionStatusUnanswered {
			continue
		}

		fmt.Fprintf(os.Stderr, "  %s: %s\n", q.ID, q.Title)
		fmt.Fprintf(os.Stderr, "  Question: %s\n", q.QuestionText)
		fmt.Fprintf(os.Stderr, "  Answer: ")

		if !scanner.Scan() {
			break
		}

		answer := strings.TrimSpace(scanner.Text())
		if answer == "" {
			fmt.Fprintf(os.Stderr, "  (skipped)\n\n")
			continue
		}

		questions[i].Status = QuestionStatusAnswered
		questions[i].Answer = answer
		modified = true
		fmt.Fprintf(os.Stderr, "  ✓ answered\n\n")
	}

	if modified {
		if err := WriteQuestions(spec.QuestionsPath, questions); err != nil {
			return fmt.Errorf("writing questions: %w", err)
		}
	}

	return nil
}

// resolveQuestion sends a question to the LLM for resolution.
func resolveQuestion(
	ctx context.Context,
	client opencode.LLMClient,
	model opencode.ModelRef,
	directory string,
	question string,
	specContext string,
) (string, error) {
	session, err := client.CreateSession(ctx, "question-resolve", directory)
	if err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}
	defer client.DeleteSession(ctx, session.ID, directory)

	data := map[string]string{
		"question":     question,
		"context":      "Source: auto-resolution during spec questions command",
		"spec_context": specContext,
	}

	prompt, err := prompts.Execute("question-resolve.md", data)
	if err != nil {
		return "", fmt.Errorf("rendering question-resolve prompt: %w", err)
	}

	resp, err := client.SendPrompt(ctx, session.ID, prompt, model, directory)
	if err != nil {
		return "", fmt.Errorf("sending prompt: %w", err)
	}

	answer := strings.TrimSpace(resp.Content)
	if strings.Contains(answer, "CANNOT_RESOLVE") {
		return "CANNOT_RESOLVE", nil
	}

	return answer, nil
}

// validateAnswer asks a secondary model to validate an answer.
func validateAnswer(
	ctx context.Context,
	client opencode.LLMClient,
	model opencode.ModelRef,
	directory string,
	question string,
	answer string,
) (bool, error) {
	session, err := client.CreateSession(ctx, "question-validate", directory)
	if err != nil {
		return false, fmt.Errorf("creating session: %w", err)
	}
	defer client.DeleteSession(ctx, session.ID, directory)

	prompt := fmt.Sprintf(`Validate this answer to a software specification question.

Question: %s

Proposed Answer: %s

Is this answer correct, complete, and well-reasoned? Reply with exactly YES or NO followed by a brief explanation.`, question, answer)

	resp, err := client.SendPrompt(ctx, session.ID, prompt, model, directory)
	if err != nil {
		return false, fmt.Errorf("sending prompt: %w", err)
	}

	response := strings.TrimSpace(resp.Content)
	return strings.HasPrefix(strings.ToUpper(response), "YES"), nil
}
