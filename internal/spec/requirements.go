package spec

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/alanmeadows/otto/internal/config"
	"github.com/alanmeadows/otto/internal/opencode"
	"github.com/alanmeadows/otto/internal/prompts"
	"github.com/alanmeadows/otto/internal/store"
)

// generateRequirements builds and runs the requirements refinement pipeline.
func generateRequirements(
	ctx context.Context,
	client opencode.LLMClient,
	pipeline *opencode.ReviewPipeline,
	spec *Spec,
	prompt string,
	existingContent string,
	codebaseSummary string,
) (string, error) {
	data := map[string]string{
		"requirements_md":  existingContent,
		"codebase_summary": codebaseSummary,
	}

	if prompt != "" {
		data["prompt"] = prompt
	}

	// Add questions if they exist
	if spec.HasQuestions() {
		questionsContent, err := store.ReadBody(spec.QuestionsPath)
		if err == nil {
			data["questions_md"] = questionsContent
		}
	}

	// Add other existing artifacts as context
	var existingArtifacts strings.Builder
	if spec.HasResearch() {
		if content, err := store.ReadBody(spec.ResearchPath); err == nil {
			fmt.Fprintf(&existingArtifacts, "### research.md\n\n%s\n\n", content)
		}
	}
	if spec.HasDesign() {
		if content, err := store.ReadBody(spec.DesignPath); err == nil {
			fmt.Fprintf(&existingArtifacts, "### design.md\n\n%s\n\n", content)
		}
	}
	if spec.HasTasks() {
		if content, err := store.ReadBody(spec.TasksPath); err == nil {
			fmt.Fprintf(&existingArtifacts, "### tasks.md\n\n%s\n\n", content)
		}
	}
	if existingArtifacts.Len() > 0 {
		data["existing_artifacts"] = existingArtifacts.String()
	}

	rendered, err := prompts.Execute("requirements.md", data)
	if err != nil {
		return "", fmt.Errorf("rendering requirements prompt: %w", err)
	}

	contextData := map[string]string{}
	if codebaseSummary != "" {
		contextData["Codebase Summary"] = codebaseSummary
	}

	result, err := pipeline.Review(ctx, rendered, contextData)
	if err != nil {
		return "", fmt.Errorf("review pipeline: %w", err)
	}

	return result, nil
}

// SpecAdd creates a new spec from a prompt and generates initial requirements.
func SpecAdd(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	prompt string,
) (*Spec, error) {
	slug := GenerateSlug(prompt)
	slog.Info("creating spec", "slug", slug)

	// Check for duplicate slug
	existingDir := filepath.Join(specsRoot(repoDir), slug)
	if _, err := os.Stat(existingDir); err == nil {
		return nil, fmt.Errorf("spec %q already exists — use a different prompt or rename the existing spec", slug)
	}

	if err := CreateSpecDir(slug, repoDir); err != nil {
		return nil, fmt.Errorf("creating spec directory: %w", err)
	}

	spec := populatePaths(slug, repoDir)

	summary, err := AnalyzeCodebase(repoDir)
	if err != nil {
		return nil, fmt.Errorf("analyzing codebase: %w", err)
	}

	pipeline := buildReviewPipeline(client, repoDir, cfg)

	// For initial creation, use the prompt as the requirements content seed.
	result, err := generateRequirements(ctx, client, pipeline, &spec, prompt, prompt, summary.String())
	if err != nil {
		return nil, fmt.Errorf("generating requirements: %w", err)
	}

	if err := store.WriteBody(spec.RequirementsPath, result); err != nil {
		return nil, fmt.Errorf("writing requirements: %w", err)
	}

	// Extract questions from LLM output
	extractAndAppendQuestions(result, &spec)

	slog.Info("spec created", "slug", slug, "path", spec.Dir)
	return &spec, nil
}

// SpecRequirements refines the requirements document for an existing spec.
func SpecRequirements(
	ctx context.Context,
	client opencode.LLMClient,
	cfg *config.Config,
	repoDir string,
	slug string,
) error {
	spec, err := ResolveSpec(slug, repoDir)
	if err != nil {
		return err
	}

	if err := CheckPrerequisites(spec, "requirements"); err != nil {
		return err
	}

	if !spec.HasRequirements() {
		return fmt.Errorf("no requirements.md found — run 'otto spec add' first to create a spec")
	}

	// Read existing requirements content
	var existingContent string
	if spec.HasRequirements() {
		existingContent, err = store.ReadBody(spec.RequirementsPath)
		if err != nil {
			return fmt.Errorf("reading existing requirements: %w", err)
		}
	}

	summary, err := AnalyzeCodebase(repoDir)
	if err != nil {
		return fmt.Errorf("analyzing codebase: %w", err)
	}

	pipeline := buildReviewPipeline(client, repoDir, cfg)

	result, err := generateRequirements(ctx, client, pipeline, spec, "", existingContent, summary.String())
	if err != nil {
		return fmt.Errorf("generating requirements: %w", err)
	}

	if err := store.WriteBody(spec.RequirementsPath, result); err != nil {
		return fmt.Errorf("writing requirements: %w", err)
	}

	// Extract questions from output if present
	extractAndAppendQuestions(result, spec)

	slog.Info("requirements updated", "spec", spec.Slug)
	return nil
}

// extractAndAppendQuestions looks for a ===QUESTIONS=== separator in the output
// and appends any questions found to the spec's questions.md.
func extractAndAppendQuestions(output string, spec *Spec) {
	parts := strings.SplitN(output, "===QUESTIONS===", 2)
	if len(parts) < 2 {
		return
	}

	questions := strings.TrimSpace(parts[1])
	if questions == "" {
		return
	}

	// Read existing questions if any
	var existing string
	if spec.HasQuestions() {
		if content, err := store.ReadBody(spec.QuestionsPath); err == nil {
			existing = content
		}
	}

	var combined string
	if existing != "" {
		combined = existing + "\n\n" + questions
	} else {
		combined = questions
	}

	if err := store.WriteBody(spec.QuestionsPath, combined); err != nil {
		slog.Warn("failed to write questions", "error", err)
	}
}

// buildReviewPipeline creates a ReviewPipeline from config.
func buildReviewPipeline(client opencode.LLMClient, directory string, cfg *config.Config) *opencode.ReviewPipeline {
	return opencode.NewReviewPipeline(client, directory, opencode.ReviewConfig{
		Primary:   opencode.ParseModelRef(cfg.Models.Primary),
		Secondary: opencode.ParseModelRef(cfg.Models.Secondary),
		Tertiary:  modelRefPtr(cfg.Models.Tertiary),
		MaxCycles: 1,
	})
}

// modelRefPtr returns a pointer to a ModelRef if the input string is non-empty, nil otherwise.
func modelRefPtr(s string) *opencode.ModelRef {
	if s == "" {
		return nil
	}
	ref := opencode.ParseModelRef(s)
	return &ref
}

// readArtifact reads an artifact's body if it exists, returning empty string otherwise.
func readArtifact(path string) string {
	if !store.Exists(path) {
		return ""
	}
	content, err := store.ReadBody(path)
	if err != nil {
		slog.Debug("failed to read artifact", "path", path, "error", err)
		return ""
	}
	return content
}
