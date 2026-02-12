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
) (string, opencode.ReviewStats, error) {
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
		return "", opencode.ReviewStats{}, fmt.Errorf("rendering requirements prompt: %w", err)
	}

	contextData := map[string]string{}
	if codebaseSummary != "" {
		contextData["Codebase Summary"] = codebaseSummary
	}

	result, stats, err := pipeline.Review(ctx, rendered, contextData)
	if err != nil {
		return "", stats, fmt.Errorf("review pipeline: %w", err)
	}

	return result, stats, nil
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

	// Check for duplicate slug — but allow re-running if the spec directory
	// exists without a requirements.md (e.g. interrupted previous run).
	existingDir := filepath.Join(specsRoot(repoDir), slug)
	reqPath := filepath.Join(existingDir, "requirements.md")
	if _, err := os.Stat(existingDir); err == nil {
		if _, reqErr := os.Stat(reqPath); reqErr == nil {
			return nil, fmt.Errorf("spec %q already exists — use a different prompt or rename the existing spec", slug)
		}
		slog.Info("resuming incomplete spec creation", "slug", slug)
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
	result, stats, err := generateRequirements(ctx, client, pipeline, &spec, prompt, prompt, summary.String())
	if err != nil {
		return nil, fmt.Errorf("generating requirements: %w", err)
	}

	printReviewStats("Requirements", stats)

	// Split questions from requirements output before writing.
	reqContent, _ := SplitQuestions(result)

	if err := store.WriteBody(spec.RequirementsPath, reqContent); err != nil {
		return nil, fmt.Errorf("writing requirements: %w", err)
	}

	// Extract questions from LLM output
	ExtractAndAppendQuestions(result, &spec)

	// Auto-resolve any new questions.
	ResolveAndReport(ctx, client, cfg, repoDir, &spec, "requirements")

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

	result, stats, err := generateRequirements(ctx, client, pipeline, spec, "", existingContent, summary.String())
	if err != nil {
		return fmt.Errorf("generating requirements: %w", err)
	}

	printReviewStats("Requirements", stats)

	// Split questions from requirements output before writing.
	reqContent, _ := SplitQuestions(result)

	if err := store.WriteBody(spec.RequirementsPath, reqContent); err != nil {
		return fmt.Errorf("writing requirements: %w", err)
	}

	// Extract questions from output if present
	ExtractAndAppendQuestions(result, spec)

	// Auto-resolve any new questions.
	ResolveAndReport(ctx, client, cfg, repoDir, spec, "requirements")

	slog.Info("requirements updated", "spec", spec.Slug)
	return nil
}

// extractAndAppendQuestions looks for a ===QUESTIONS=== separator in the output
// and appends any questions found to the spec's questions.md.
// Moved to questions.go — this is a thin wrapper kept for readability.
// See ExtractAndAppendQuestions in questions.go for the implementation.

// buildReviewPipeline creates a ReviewPipeline from config.
func buildReviewPipeline(client opencode.LLMClient, directory string, cfg *config.Config) *opencode.ReviewPipeline {
	return opencode.NewReviewPipeline(client, directory, opencode.ReviewConfig{
		Primary:   opencode.ParseModelRef(cfg.Models.Primary),
		Secondary: opencode.ParseModelRef(cfg.Models.Secondary),
		MaxCycles: 1,
	})
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
